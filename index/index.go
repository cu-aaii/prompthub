package index

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	
)

type PromptIndex map[string]*Prompt

var prompts PromptIndex
var cards map[string]string

func GetPrompt(name string) (*Prompt, error) {
	val, ok := prompts[name]
	if ok {
		return val, nil
	}
	return nil, errors.New("not found")
}

func GetPrompts() []*Prompt {
	var prompts []*Prompt

	// Read from existing YAML files
	files, err := ioutil.ReadDir("prompts")
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".yaml" {
			prompt, err := readPromptFromFile(filepath.Join("prompts", file.Name()))
			if err != nil {
				log.Printf("Error reading prompt from %s: %v", file.Name(), err)
				continue
			}
			prompts = append(prompts, prompt)
		}
	}

	return prompts
}

func GetCard(name string) (string, error) {
	val, ok := cards[name]
	if ok {
		return val, nil
	}
	return "", errors.New("not found")
}

func Init(path string) error {

	files, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	prompts = PromptIndex{}
	cards = map[string]string{}

	for _, file := range files {
		// yaml files only
		ext := strings.ToLower(filepath.Ext(file.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		var p Prompt
		yamlFile, err := os.ReadFile(filepath.Join(path, file.Name()))
		if err != nil {
			log.Printf("Readfile error:  %v", err)
		}
		err = yaml.Unmarshal(yamlFile, &p)
		if err != nil {
			log.Fatalf("Unmarshal %s: %v", file.Name(), err)
		}

		prompts[p.Name] = &p

		// If the prompt has a related markdown with more detailed explanation
		// add it to a separate index. Note we use the prompt file name 
		// as the name of the card, not the actual name of the prompt.
		cardName := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
		cardFile := filepath.Join(path, fmt.Sprintf("%s.md", cardName))
		if _, err := os.Stat(cardFile); errors.Is(err, os.ErrNotExist) {
			log.Printf("Card not found for prompt %s", p.Name)
			continue
		}
		cardData, err := os.ReadFile(cardFile)
		if err != nil {
			log.Printf("Readfile error:  %v", err)
		}
		cards[p.Name] = string(cardData)
	}

	return nil
}

// Add this function
func readPromptFromFile(filePath string) (*Prompt, error) {
	yamlFile, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file error: %v", err)
	}

	var prompt Prompt
	err = yaml.Unmarshal(yamlFile, &prompt)
	if err != nil {
		return nil, fmt.Errorf("unmarshal error: %v", err)
	}

	return &prompt, nil
}
