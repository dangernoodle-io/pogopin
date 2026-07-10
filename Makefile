.PHONY: build test cover lint hwbench-check mock-bench mcp-mock acc fmt tidy clean install

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

mock-bench:
	ACC_POGOPIN=1 go test ./test/hwbench/ -run TestMockBench -v -timeout 300s

mcp-mock:
	ACC_POGOPIN=1 go test ./internal/mcpserver/ -run TestMock -v -timeout 120s

acc: mock-bench mcp-mock

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

clean:
	rm -f pogo coverage.out
