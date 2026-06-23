BINARY := morgward
PKG := ./cmd/morgward
LDFLAGS := -s -w

.PHONY: build vet fmt clean release package-linux winres

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

# Regenerate the embedded Windows icon resource from assets/icons.
# Requires go-winres (go install github.com/tc-hib/go-winres@latest).
# The resulting cmd/morgward/rsrc_windows_*.syso is committed; `go build` links
# it automatically for windows targets, so casual builds need not run this.
winres:
	cd $(PKG) && go-winres make --in winres/winres.json --arch amd64,arm64 --out rsrc

# Assemble Linux desktop-integration tarballs (binary + .desktop + hicolor icons
# + install/uninstall). Output goes under dist/desktop/ so it never pollutes the
# dist/morgward-* glob that checksums.txt (go-selfupdate contract) keys off.
# Run after `release` (it consumes the linux binaries it produced).
package-linux:
	mkdir -p dist/desktop
	for arch in amd64 arm64; do \
	  stage=$$(mktemp -d); \
	  mkdir -p $$stage/$(BINARY); \
	  cp dist/$(BINARY)-linux-$$arch                $$stage/$(BINARY)/$(BINARY); \
	  chmod 755                                     $$stage/$(BINARY)/$(BINARY); \
	  cp packaging/linux/morgward.desktop           $$stage/$(BINARY)/; \
	  cp packaging/linux/install.sh                 $$stage/$(BINARY)/; \
	  cp packaging/linux/uninstall.sh               $$stage/$(BINARY)/; \
	  chmod 755 $$stage/$(BINARY)/install.sh $$stage/$(BINARY)/uninstall.sh; \
	  cp -r packaging/linux/icons                   $$stage/$(BINARY)/; \
	  tar -C $$stage -czf dist/desktop/$(BINARY)-linux-$$arch-desktop.tar.gz $(BINARY); \
	  rm -rf $$stage; \
	  echo "packaged dist/desktop/$(BINARY)-linux-$$arch-desktop.tar.gz"; \
	done
