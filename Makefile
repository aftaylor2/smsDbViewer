APP_NAME := smsDbViewer
GO_FILES := $(shell find . -name '*.go' -not -path './vendor/*')

.PHONY: build clean run tidy test

build: $(APP_NAME)

$(APP_NAME): $(GO_FILES) go.mod go.sum
	go build -o $(APP_NAME) .

run: build
	./$(APP_NAME)

tidy:
	go mod tidy

test:
	go test -v ./...

clean:
	rm -f $(APP_NAME)
