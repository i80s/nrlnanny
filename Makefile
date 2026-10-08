# Makefile for building nrlnanny for different platforms and architectures

# Variables
BIN_DIR = bin

# Targets
all: linux_arm64

windows_x86_64:
	@echo "Building Windows x86_64 binary..."
	@mkdir -p $(BIN_DIR)
	@GOOS=windows GOARCH=amd64 go build -o $(BIN_DIR)/nrlnanny_windows_x86_64.exe

# windows_arm64:
# 	@echo "Building Windows ARM64 binary..."
# 	@mkdir -p $(BIN_DIR)
# 	@GOOS=windows GOARCH=arm64 go build -o $(BIN_DIR)/nrlnanny_windows_arm64.exe

linux_x86_64:
	@echo "Building Linux x86_64 binary..."
	@mkdir -p $(BIN_DIR)
	@GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/nrlnanny_linux_x86_64

linux_arm64:
	@echo "Building Linux ARM64 binary..."
	@mkdir -p $(BIN_DIR)
	@GOOS=linux GOARCH=arm64 go build -o $(BIN_DIR)/nrlnanny_linux_arm64

# linux_arm32:
# 	@echo "Building Linux ARM32 binary..."
# 	@mkdir -p $(BIN_DIR)
# 	@GOOS=linux GOARCH=arm go build -o $(BIN_DIR)/nrlnanny_linux_arm32

install:
	cp $(BIN_DIR)/nrlnanny_linux_arm64 /opt/nrlnanny/nrlnanny

clean:
	@echo "Cleaning up..."
	@rm -rf $(BIN_DIR)
