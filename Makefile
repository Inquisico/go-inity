.PHONY: all update lint test bench coverage run build

all: test

update:
	@go mod tidy \
		&& go work vendor

lint:
	@golangci-lint run ./...

test:
	@go test ./... -coverprofile=tmp/coverage.out

bench:
	@go test -bench=. ./... -coverprofile=tmp/coverage.out

coverage:
	@go tool cover -html=tmp/coverage.out

mock:
	@mockery --name ".*" --case underscore --exported --with-expecter --output ./tmp/mocks --dir $(DIR) \
		&& rm -rf $(DIR)/mocks \
		&& mv tmp/mocks $(DIR)

mockall:
	@mockery --all --keeptree --case underscore --exported --with-expecter --output ./tmp/mocks --dir $(DIR) \
		&& find tmp/mocks -type d -depth -exec bash -c 'rm -rf $(DIR)$${1#tmp/mocks}/mocks && mv $$1 $(DIR)$${1#tmp/mocks}/mocks' _ {} \;

run: update
	@go run ./examples/simple
