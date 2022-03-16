VERSION := $(shell git describe --tags | sed -ne 's/[^0-9]*\(\([0-9]\.\)\{0,4\}[0-9]\)[^.].*/\1/p')
REMOTE := $(shell git remote -v | sed -n '/github.com.*push/{s/^[^[:space:]]\+[[:space:]]git@github.com\+//;s|:||;s/\.git.*//;p}')

docker-build:
	./tools/docker-buildx \
		build \
			--platform linux/arm64 \
			-f ./Dockerfile \
			-t ghcr.io/$(REMOTE):$(VERSION) .
