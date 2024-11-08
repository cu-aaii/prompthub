FROM golang:1.20-alpine AS build

ENV CGO_ENABLED=0
WORKDIR /go/src/github.com/deepset-ai/prompthub
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o prompthub

FROM alpine:3.17

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=build /go/src/github.com/deepset-ai/prompthub/prompthub /usr/bin/prompthub
# In case you don't want to run the service wit default values,
# put your configuration in the example file and uncomment the
# following line:
# COPY prompthub.yaml.example /prompthub.yaml
COPY prompts ./prompts
COPY prompthub.yaml.example /prompthub.yaml

EXPOSE 80

CMD ["prompthub", "-c", "/prompthub.yaml"]
