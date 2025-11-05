APP=localflow

.PHONY: run test fmt tidy

run:
	go run ./cmd/localflow

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

test:
	go test ./... -race -count=1