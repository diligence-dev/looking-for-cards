.PHONY: test build run

test:
	CGO_ENABLED=1 go test ./server/tests/

build:
	go build -o looking-for-cards .

run: build
	./looking-for-cards
