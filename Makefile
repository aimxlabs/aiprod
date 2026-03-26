.PHONY: build run init test clean docker

BINARY = aiprod

build:
	go build -o $(BINARY) ./cmd/aiprod

run: build
	./$(BINARY) serve

init: build
	./$(BINARY) init

test:
	go test ./...

clean:
	rm -f $(BINARY)
	rm -rf data/

docker:
	docker compose -f docker/docker-compose.yml up --build

docker-down:
	docker compose -f docker/docker-compose.yml down

fmt:
	go fmt ./...

vet:
	go vet ./...
