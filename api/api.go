package api

import (
	"context"
	"encoding/json"
	fmt "fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/google/go-github/v45/github"
	"golang.org/x/oauth2"

	"github.com/deepset-ai/prompthub/index"
	"github.com/deepset-ai/prompthub/output"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/go-chi/render"
	"github.com/spf13/viper"
	"gopkg.in/gomail.v2"
	"github.com/go-git/go-git/v5"
)

// Serve starts the HTTP server and blocks
func Serve() {
	r := chi.NewRouter() // root router
	r.Use(render.SetContentType(render.ContentTypeJSON))
	// r.Use(cors.Handler(cors.Options{
	// 	AllowedOrigins: viper.GetStringSlice("allowed_origins"),
	// }))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"}, // Allow all origins
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type", "Origin"},
	}))
	output.DEBUG.Printf("AllowedOrigins set to: %s", viper.GetStringSlice("allowed_origins"))

	promptsRouter := chi.NewRouter()
	promptsRouter.Get("/", ListPrompts)
	promptsRouter.Get("/*", GetPrompt)
	promptsRouter.Post("/request", HandleNewPromptRequest)
	promptsRouter.Post("/github-webhook", HandleGitHubWebhook)
	r.Post("/reload-prompts", HandleReloadPrompts)

	r.Mount("/prompts", promptsRouter)

	promptCardRouter := chi.NewRouter()
	promptCardRouter.Get("/*", GetCard)

	r.Mount("/cards", promptCardRouter)

	// Start the HTTP server
	srv := &http.Server{
		Addr: fmt.Sprintf("0.0.0.0:%s", viper.GetString("port")),
		// Set timeouts to avoid Slowloris attacks.
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      r,
	}

	// Run our server in a goroutine so that it doesn't block.
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			output.FATAL.Println(err)
		}
	}()

	output.INFO.Println("Prompthub running at", srv.Addr)

	c := make(chan os.Signal, 1)
	// We'll accept graceful shutdowns when quit via SIGINT (Ctrl+C)
	// SIGKILL, SIGQUIT or SIGTERM (Ctrl+/) will not be caught.
	signal.Notify(c, os.Interrupt)

	// Block until we receive our signal.
	<-c

	// Create a deadline to wait for.
	ctx, cancel := context.WithTimeout(context.Background(), 10)
	defer cancel()
	// Doesn't block if no connections, but will otherwise wait
	// until the timeout deadline.
	srv.Shutdown(ctx)
	output.INFO.Println("shutting down")
}

func ListPrompts(w http.ResponseWriter, r *http.Request) {
	prompts := index.GetPrompts()
	if err := render.RenderList(w, r, NewPromptListResponse(prompts)); err != nil {
		render.Render(w, r, ErrRender(err))
		return
	}
}

func NewPromptListResponse(prompts []*index.Prompt) []render.Renderer {
	list := []render.Renderer{}
	for _, prompt := range prompts {
		list = append(list, NewPromptResponse(prompt))
	}
	return list
}

func GetPrompt(w http.ResponseWriter, r *http.Request) {
	promptName := chi.URLParam(r, "*")
	prompt, err := index.GetPrompt(promptName)
	if err != nil {
		render.Render(w, r, ErrNotFound)
		return
	}

	if err := render.Render(w, r, NewPromptResponse(prompt)); err != nil {
		render.Render(w, r, ErrRender(err))
		return
	}
}

func GetCard(w http.ResponseWriter, r *http.Request) {
	promptName := chi.URLParam(r, "*")
	card, err := index.GetCard(promptName)
	if err != nil {
		render.Render(w, r, ErrNotFound)
		return
	}
	render.PlainText(w, r, card)
}

// New struct to represent a prompt request
type PromptRequest struct {
	Name        string `json:"name"`
	Text        string `json:"text"`
	Description string `json:"description"`
	Tags        string `json:"tags"`
	Meta        struct {
		Author      string `json:"author"`
		Institution string `json:"institution"`
	} `json:"meta"`
}

func HandleNewPromptRequest(w http.ResponseWriter, r *http.Request) {
	var promptRequest PromptRequest
	if err := json.NewDecoder(r.Body).Decode(&promptRequest); err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	// TODO: Validate the request data

	// Create GitHub PR for the new prompt
	err := createGitHubPR(promptRequest)
	if err != nil {
		render.Render(w, r, ErrInternalServer(err))
		return
	}

	render.Status(r, http.StatusCreated)
	render.JSON(w, r, map[string]string{"message": "Prompt request submitted successfully"})
}

func createGitHubPR(request PromptRequest) error {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: viper.GetString("github_token")},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Get the SHA of the latest commit on the base branch
	baseBranch := "main" // or "master", depending on your repository
	ref, _, err := client.Git.GetRef(ctx, "cu-aaii", "prompthub", "refs/heads/"+baseBranch)
	if err != nil {
		return fmt.Errorf("error getting base branch reference: %v", err)
	}

	// Create a new branch
	branchName := fmt.Sprintf("prompt-request-%s", time.Now().Format("20060102150405"))
	newRef := &github.Reference{
		Ref: github.String("refs/heads/" + branchName),
		Object: &github.GitObject{
			SHA: ref.Object.SHA,
		},
	}
	_, _, err = client.Git.CreateRef(ctx, "cu-aaii", "prompthub", newRef)
	if err != nil {
		output.ERROR.Printf("GitHub API error: %v", err)
		return fmt.Errorf("error creating branch: %v", err)
	}
	// Create YAML content
	yamlContent := fmt.Sprintf(`name: %s
text: |
  %s
description: %s
tags: 
  - %s
meta:
  author: 
    - %s
  institution: %s
`, request.Name, request.Text, request.Description, request.Tags, request.Meta.Author, request.Meta.Institution)

	// Create or update file in the new branch
	filePath := fmt.Sprintf("prompts/%s.yaml", request.Name)
	_, _, err = client.Repositories.CreateFile(ctx, "cu-aaii", "prompthub", filePath, &github.RepositoryContentFileOptions{
		Message: github.String(fmt.Sprintf("Add new prompt: %s", request.Name)),
		Content: []byte(yamlContent),
		Branch:  github.String(branchName),
	})
	if err != nil {
		return fmt.Errorf("error creating file: %v", err)
	}

	// Create pull request
	newPR, _, err := client.PullRequests.Create(ctx, "cu-aaii", "prompthub", &github.NewPullRequest{
		Title: github.String(fmt.Sprintf("New Prompt Request: %s", request.Name)),
		Head:  github.String(branchName),
		Base:  github.String(baseBranch),
		Body:  github.String(fmt.Sprintf("New prompt request from %s (%s)", request.Name, request.Text)),
	})
	if err != nil {
		return fmt.Errorf("error creating pull request: %v", err)
	}

	output.INFO.Printf("Created pull request #%d", newPR.GetNumber())
	return nil
}

// Add this function near the other error handlers
func ErrInternalServer(err error) render.Renderer {
	return &ErrResponse{
		Err:            err,
		HTTPStatusCode: http.StatusInternalServerError,
		StatusText:     "Internal Server Error",
		ErrorText:      err.Error(),
	}
}

// Add this function to send emails
func sendEmail(to, subject, body string) error {
	m := gomail.NewMessage()
	m.SetHeader("From", "your-email@example.com")
	m.SetHeader("To", to)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", body)

	d := gomail.NewDialer("smtp.example.com", 587, "your-email@example.com", "your-password")

	return d.DialAndSend(m)
}

// Add a new handler for GitHub webhooks
func HandleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := github.ValidatePayload(r, []byte("your-webhook-secret"))
	if err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		http.Error(w, "Unable to parse webhook", http.StatusBadRequest)
		return
	}

	switch e := event.(type) {
	case *github.PullRequestEvent:
		if e.GetAction() == "closed" && e.PullRequest.GetMerged() {
			// PR was merged
			go syncRepository() // Run this in a goroutine to not block the response
			sendEmail(e.PullRequest.User.GetEmail(), "Your prompt was approved", "Congratulations! Your prompt has been approved and merged.")
		} else if e.GetAction() == "closed" && !e.PullRequest.GetMerged() {
			// PR was closed without merging
			sendEmail(e.PullRequest.User.GetEmail(), "Your prompt was not approved", "We're sorry, but your prompt was not approved at this time.")
		}
	}

	w.WriteHeader(http.StatusOK)
}

func syncRepository() {
	r, err := git.PlainOpen("/path/to/your/local/repository")
	if err != nil {
		output.ERROR.Printf("Failed to open repository: %v", err)
		return
	}

	w, err := r.Worktree()
	if err != nil {
		output.ERROR.Printf("Failed to get worktree: %v", err)
		return
	}

	err = w.Pull(&git.PullOptions{RemoteName: "origin"})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		output.ERROR.Printf("Failed to pull changes: %v", err)
		return
	}

	output.INFO.Printf("Repository synced successfully")
	index.ReloadPrompts() // Reload prompts after successful sync
}

func HandleReloadPrompts(w http.ResponseWriter, r *http.Request) {
	// You might want to add authentication for this endpoint
	index.ReloadPrompts()
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Prompts reloaded successfully"))
}
