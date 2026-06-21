.PHONY: build test integration lint clean fmt

ifeq ($(OS),Windows_NT)
BINARY_NAME := sqlited.exe
else
BINARY_NAME := sqlited
endif

build:
	go build -o $(BINARY_NAME) ./cmd/sqlited

test:
	go test -count=1 ./...

integration:
	go test -count=1 -tags=integration ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:" && gofmt -l . && exit 1)

# Race detector requires cgo. Use only if you are willing to enable CGO.
test-race:
	go test -race -count=1 ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME).exe
	rm -f sqlited-*.exe sqlited-*
