# Binary name and build directory
binary_name := "pane-patrol"
build_dir := "bin"

# Show available recipes
default:
    @just --list

# Build the binary
build:
    mkdir -p {{build_dir}}
    go build -o {{build_dir}}/{{binary_name}} .

# Install to /usr/local/bin (requires sudo)
install: build
    sudo cp {{build_dir}}/{{binary_name}} /usr/local/bin/

# Install to ~/bin (no sudo required)
install-user: build
    mkdir -p ~/bin
    cp {{build_dir}}/{{binary_name}} ~/bin/
    @echo "Make sure ~/bin is in your PATH"

# Clean build artifacts
clean:
    go clean
    rm -rf {{build_dir}}

# Run unit tests
test:
    go test -v -short ./...

# Run all tests with race detection
test-race:
    go test -v -race ./...

# Cross-compile for all platforms
build-all:
    mkdir -p {{build_dir}}
    GOOS=linux GOARCH=amd64 go build -o {{build_dir}}/{{binary_name}}-linux-amd64 .
    GOOS=linux GOARCH=arm64 go build -o {{build_dir}}/{{binary_name}}-linux-arm64 .
    GOOS=darwin GOARCH=amd64 go build -o {{build_dir}}/{{binary_name}}-darwin-amd64 .
    GOOS=darwin GOARCH=arm64 go build -o {{build_dir}}/{{binary_name}}-darwin-arm64 .

# Run linter
lint:
    golangci-lint run

# Install hook adapters for supported assistants
install-hooks:
	./scripts/install-hooks.sh
