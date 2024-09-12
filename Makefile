.PHONY: clean build run deploy generate_embeds

include .env

B := bin
BINARY = grunner$(subst $(SPACE),-,$(subst $(SPACE)$(SPACE),-,$(SUFFIX)))
VERSION ?= 1.3.1
QEMU_PATH ?= qemu-system-i386

EMPTY :=
SPACE := $(EMPTY) $(EMPTY)
SUFFIX := $(SPACE)

OUT_FILE = $(B)/$(BINARY)

# if there are any uncommitted changes OR the branch is not main, add -edge suffix
GIT_STATUS := $(shell git status --porcelain)
CURRENT_BRANCH := $(shell git rev-parse --abbrev-ref HEAD)
ifneq ($(GIT_STATUS)$(CURRENT_BRANCH),main)
	VERSION := $(VERSION)-$(shell git describe --tags --always --dirty)
	SUFFIX += edge
else
	VERSION := $(VERSION)-$(shell git describe --tags --always)
endif

all: build

clean:
	rm -rf $(B)


#generate_embeds:
#	@mkdir -p $(B)
#	@echo $(VERSION) > $(B)/.version
#	@echo $(QEMU_PATH) > $(B)/.qemu_path

GO_FILES := $(shell find . -name '*.go')

$(OUT_FILE) : $(GO_FILES)
	go build -o $(OUT_FILE) -ldflags "-X 'main.Version=$(VERSION)' -X main.QemuPath=$(QEMU_PATH)"

build : $(OUT_FILE)

build/amd64 : export GOOS=linux
build/amd64 : export GOARCH=amd64
build/amd64 : SUFFIX += linux
build/amd64 : $(OUT_FILE)

run:
	go run main.go

deploy : build/amd64
	@echo "Deploying to $(DEPLOY_SSH_HOST)"
	rsync $(OUT_FILE)-linux $(DEPLOY_SSH_HOST):$(DEPLOY_PATH)/$(BINARY)