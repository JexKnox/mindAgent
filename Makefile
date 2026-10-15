.PHONY: build run test vet tidy clean

build:
	go build -o bin/mindagent ./cmd/mindagent

run:
	go run ./cmd/mindagent

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin
