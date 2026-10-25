BINARY = dicam
ifeq ($(OS),Windows_NT)
BINARY = dicam.exe
endif
.PHONY: all ffmpeg build test vet check-linkage
all: build
ffmpeg:
	bash ./scripts/build-ffmpeg.sh
build: ffmpeg
	bash ./scripts/go.sh build -o "$(BINARY)" .
	$(MAKE) check-linkage
check-linkage:
	bash ./scripts/check-linkage.sh "$(BINARY)"
test: ffmpeg
	bash ./scripts/go.sh test -race ./...
vet: ffmpeg
	bash ./scripts/go.sh vet ./...
