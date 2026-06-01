BINARY := morgward
PKG := ./cmd/morgward
LDFLAGS := -s -w

.PHONY: build vet fmt clean release

build:
	go build -o $(BINARY) $(PKG)

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf dist $(BINARY) $(BINARY).exe

release:
	mkdir -p dist
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-linux-amd64        $(PKG)
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-linux-arm64        $(PKG)
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-darwin-amd64       $(PKG)
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-darwin-arm64       $(PKG)
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-windows-amd64.exe  $(PKG)
	cd dist && sha256sum --text $(BINARY)-* > checksums.txt
