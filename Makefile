.PHONY: build test cover lint hwbench-check fmt tidy clean install

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

hwbench-check:
	go build -tags hwtest ./...
	go vet -tags hwtest ./...
	golangci-lint run --build-tags hwtest ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

clean:
	rm -f pogo coverage.out
