.PHONY: build test cover lint fmt tidy clean install

build:
	CGO_ENABLED=0 go build -o pogo .

install:
	go install .

test:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

clean:
	rm -f pogo coverage.out
