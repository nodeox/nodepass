.PHONY: all build test clean install proto lint fmt deps run bench test-e2e build-all

# 变量
BINARY_NAME=nodepass
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

all: lint test build

# 构建
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) cmd/nodepass/main.go

# 交叉编译
build-all:
	@echo "Building for all platforms..."
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-amd64 cmd/nodepass/main.go
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-arm64 cmd/nodepass/main.go
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-amd64 cmd/nodepass/main.go
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-arm64 cmd/nodepass/main.go
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-windows-amd64.exe cmd/nodepass/main.go
	@echo "Build complete!"

# 测试
test:
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./...
	@echo "Coverage report:"
	@go tool cover -func=coverage.out | tail -1

# 基准测试
bench:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./...

# 集成测试
test-e2e:
	@echo "Running e2e tests..."
	go test -v ./test/e2e/...

# 代码检查
lint:
	@echo "Running linters..."
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run

# 生成 protobuf
proto:
	@echo "Generating protobuf..."
	@which protoc > /dev/null || (echo "protoc not found, please install it" && exit 1)
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/*.proto

# 清理
clean:
	@echo "Cleaning..."
	rm -rf bin/ coverage.out

# 安装
install:
	@echo "Installing..."
	go install $(LDFLAGS) cmd/nodepass/main.go

# 运行
run:
	@echo "Running $(BINARY_NAME)..."
	go run cmd/nodepass/main.go

# 格式化
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@which goimports > /dev/null && goimports -w . || echo "goimports not found, skipping"

# 依赖更新
deps:
	@echo "Installing dependencies..."
	go get github.com/quic-go/quic-go
	go get github.com/hashicorp/yamux
	go get go.uber.org/zap
	go get github.com/prometheus/client_golang/prometheus
	go get github.com/prometheus/client_golang/prometheus/promauto
	go get go.opentelemetry.io/otel
	go get go.opentelemetry.io/otel/trace
	go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
	go get github.com/google/uuid
	go get gopkg.in/yaml.v3
	go get google.golang.org/grpc
	go get google.golang.org/protobuf/cmd/protoc-gen-go
	go get google.golang.org/grpc/cmd/protoc-gen-go-grpc
	go get github.com/stretchr/testify
	go get go.uber.org/goleak
	go mod tidy
	@echo "Dependencies installed!"

# 开发工具安装
tools:
	@echo "Installing development tools..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	@echo "Tools installed!"

# 帮助
help:
	@echo "NodePass Makefile Commands:"
	@echo "  make build       - Build the binary"
	@echo "  make build-all   - Build for all platforms"
	@echo "  make test        - Run tests with coverage"
	@echo "  make bench       - Run benchmarks"
	@echo "  make test-e2e    - Run e2e tests"
	@echo "  make lint        - Run linters"
	@echo "  make fmt         - Format code"
	@echo "  make deps        - Install dependencies"
	@echo "  make tools       - Install development tools"
	@echo "  make proto       - Generate protobuf code"
	@echo "  make run         - Run the application"
	@echo "  make clean       - Clean build artifacts"
	@echo "  make install     - Install binary"
