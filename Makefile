version = 1.1
bin = ../bin
ldflags = -s -w -X main.build_version=$(version)
go_sources := $(shell find server game games -name '*.go') go.mod go.sum

# World versions are two-part X.Y: major (2.0, 3.0, ...) for releases with
# new features players would care about, minor (1.1, 1.2, ...) for bug fixes
# and minor features they wouldn't.
major = $(word 1,$(subst ., ,$(version)))

JOBS ?= $(shell nproc)
timing = /tmp/mochi-world-release-timing.txt

build_linux_amd64 = /tmp/mochi-world_$(version)_linux_amd64
build_linux_arm64 = /tmp/mochi-world_$(version)_linux_arm64
build_linux_armhf = /tmp/mochi-world_$(version)_linux_armhf
deb_amd64 = $(build_linux_amd64).deb
deb_arm64 = $(build_linux_arm64).deb
deb_armhf = $(build_linux_armhf).deb
rpm_x86_64 = /tmp/mochi-world-$(version)-1.x86_64.rpm
rpm_aarch64 = /tmp/mochi-world-$(version)-1.aarch64.rpm
rpm_armv7hl = /tmp/mochi-world-$(version)-1.armv7hl.rpm
rpmbuild_x86_64  = /tmp/mochi-world-rpmbuild-x86_64
rpmbuild_aarch64 = /tmp/mochi-world-rpmbuild-aarch64
rpmbuild_armv7hl = /tmp/mochi-world-rpmbuild-armv7hl
build_windows = /tmp/mochi-world_$(version)_windows_amd64
msi = $(build_windows).msi
pkg_amd64 = /tmp/mochi-world_$(version)_darwin_amd64.pkg
pkg_arm64 = /tmp/mochi-world_$(version)_darwin_arm64.pkg
docker_image = ghcr.io/mochi-os/mochi-world

all: $(bin)/mochi-world

$(bin)/mochi-world: $(go_sources) | $(bin)
	CGO_ENABLED=0 go build -v -ldflags "$(ldflags)" -o $(bin)/mochi-world ./server

$(bin):
	mkdir -p $(bin)

run1: all
	$(bin)/mochi-world -f /etc/mochi/world1.conf

test:
	go test ./...

# Run the simulation-core tests on the browser target: the golden-trace
# comparison under wasm IS the native-versus-wasm divergence bound. The
# battle package rides along — the single-player client judges damage with
# the same Go via wasm, so its tests passing there pin that parity claim.
test-wasm:
	GOOS=js GOARCH=wasm PATH="$(shell go env GOROOT)/lib/wasm:$$PATH" go test $(testflags) ./games/air/flight/ ./games/air/battle/

# Compile-check the simulation core and its boundary for the browser target.
wasm:
	GOOS=js GOARCH=wasm CGO_ENABLED=0 go build ./games/air/flight/ ./wasm/

clean:
	rm -f $(bin)/mochi-world $(bin)/mochi-world-linux-arm64 $(bin)/mochi-world-linux-arm $(bin)/mochi-world.exe $(bin)/mochi-world-darwin-amd64 $(bin)/mochi-world-darwin-arm64 $(bin)/mochi-world.8

# --------------------------------------------------------------------------
# Cross-compile binaries (pure Go, CGO off — no cross-toolchains needed)
# --------------------------------------------------------------------------

$(bin)/mochi-world-linux-arm64: $(go_sources) | $(bin)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -v -ldflags "$(ldflags)" -o $(bin)/mochi-world-linux-arm64 ./server

$(bin)/mochi-world-linux-arm: $(go_sources) | $(bin)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -v -ldflags "$(ldflags)" -o $(bin)/mochi-world-linux-arm ./server

$(bin)/mochi-world.exe: $(go_sources) | $(bin)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -v -ldflags "$(ldflags)" -o $(bin)/mochi-world.exe ./server

$(bin)/mochi-world-darwin-amd64: $(go_sources) | $(bin)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -v -ldflags "$(ldflags)" -o $(bin)/mochi-world-darwin-amd64 ./server

$(bin)/mochi-world-darwin-arm64: $(go_sources) | $(bin)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -v -ldflags "$(ldflags)" -o $(bin)/mochi-world-darwin-arm64 ./server

# Man page: docs/mochi-world.8.md -> $(bin)/mochi-world.8 via pandoc.
# Requires: apt install pandoc
# raw_html is disabled so <placeholder> text renders literally instead of
# being dropped as an unknown HTML tag.
$(bin)/mochi-world.8: docs/mochi-world.8.md | $(bin)
	pandoc -s -f markdown-raw_html -t man docs/mochi-world.8.md -o $(bin)/mochi-world.8

# --------------------------------------------------------------------------
# .deb packages
# --------------------------------------------------------------------------

$(deb_amd64): $(bin)/mochi-world $(bin)/mochi-world.8
	mkdir -p -m 0775 $(build_linux_amd64) $(build_linux_amd64)/usr/sbin $(build_linux_amd64)/var/lib/mochi-world
	cp -av build/deb/* $(build_linux_amd64)
	sed 's/_VERSION_/$(version)/' build/deb/DEBIAN/control > $(build_linux_amd64)/DEBIAN/control
	cp -av install/* $(build_linux_amd64)
	cp -av $(bin)/mochi-world $(build_linux_amd64)/usr/sbin
	upx -1 -qq $(build_linux_amd64)/usr/sbin/mochi-world
	mkdir -p $(build_linux_amd64)/usr/share/man/man8
	cp $(bin)/mochi-world.8 $(build_linux_amd64)/usr/share/man/man8/
	dpkg-deb -Zxz -z9 --build --root-owner-group $(build_linux_amd64)
	rm -rf $(build_linux_amd64)
	ls -l $(deb_amd64)

deb-amd64: $(deb_amd64)

$(deb_arm64): $(bin)/mochi-world-linux-arm64 $(bin)/mochi-world.8
	mkdir -p -m 0775 $(build_linux_arm64) $(build_linux_arm64)/usr/sbin $(build_linux_arm64)/var/lib/mochi-world
	cp -av build/deb/* $(build_linux_arm64)
	sed -e 's/_VERSION_/$(version)/' -e 's/Architecture: amd64/Architecture: arm64/' build/deb/DEBIAN/control > $(build_linux_arm64)/DEBIAN/control
	cp -av install/* $(build_linux_arm64)
	cp -av $(bin)/mochi-world-linux-arm64 $(build_linux_arm64)/usr/sbin/mochi-world
	mkdir -p $(build_linux_arm64)/usr/share/man/man8
	cp $(bin)/mochi-world.8 $(build_linux_arm64)/usr/share/man/man8/
	dpkg-deb -Zxz -z9 --build --root-owner-group $(build_linux_arm64)
	rm -rf $(build_linux_arm64)
	ls -l $(deb_arm64)

deb-arm64: $(deb_arm64)

$(deb_armhf): $(bin)/mochi-world-linux-arm $(bin)/mochi-world.8
	mkdir -p -m 0775 $(build_linux_armhf) $(build_linux_armhf)/usr/sbin $(build_linux_armhf)/var/lib/mochi-world
	cp -av build/deb/* $(build_linux_armhf)
	sed -e 's/_VERSION_/$(version)/' -e 's/Architecture: amd64/Architecture: armhf/' build/deb/DEBIAN/control > $(build_linux_armhf)/DEBIAN/control
	cp -av install/* $(build_linux_armhf)
	cp -av $(bin)/mochi-world-linux-arm $(build_linux_armhf)/usr/sbin/mochi-world
	mkdir -p $(build_linux_armhf)/usr/share/man/man8
	cp $(bin)/mochi-world.8 $(build_linux_armhf)/usr/share/man/man8/
	dpkg-deb -Zxz -z9 --build --root-owner-group $(build_linux_armhf)
	rm -rf $(build_linux_armhf)
	ls -l $(deb_armhf)

deb-armhf: $(deb_armhf)

deb: deb-amd64 deb-arm64 deb-armhf

# --------------------------------------------------------------------------
# .rpm packages
# --------------------------------------------------------------------------

# Requires: apt install rpm
$(rpm_x86_64): $(bin)/mochi-world $(bin)/mochi-world.8
	rm -rf $(rpmbuild_x86_64)
	mkdir -p $(rpmbuild_x86_64)/SOURCES $(rpmbuild_x86_64)/SPECS $(rpmbuild_x86_64)/BUILD $(rpmbuild_x86_64)/RPMS $(rpmbuild_x86_64)/SRPMS
	cp $(bin)/mochi-world $(rpmbuild_x86_64)/SOURCES/
	cp $(bin)/mochi-world.8 $(rpmbuild_x86_64)/SOURCES/
	cp install/etc/mochi/world.conf $(rpmbuild_x86_64)/SOURCES/
	cp install/etc/systemd/system/mochi-world.service $(rpmbuild_x86_64)/SOURCES/
	rpmbuild -bb --define "_topdir $(rpmbuild_x86_64)" --define "_version $(version)" --target x86_64 build/rpm/mochi-world.spec
	cp $(rpmbuild_x86_64)/RPMS/x86_64/mochi-world-$(version)-1.x86_64.rpm $(rpm_x86_64)
	rm -rf $(rpmbuild_x86_64)
	ls -l $(rpm_x86_64)

rpm-x86_64: $(rpm_x86_64)

$(rpm_aarch64): $(bin)/mochi-world-linux-arm64 $(bin)/mochi-world.8
	rm -rf $(rpmbuild_aarch64)
	mkdir -p $(rpmbuild_aarch64)/SOURCES $(rpmbuild_aarch64)/SPECS $(rpmbuild_aarch64)/BUILD $(rpmbuild_aarch64)/RPMS $(rpmbuild_aarch64)/SRPMS
	cp $(bin)/mochi-world-linux-arm64 $(rpmbuild_aarch64)/SOURCES/mochi-world
	cp $(bin)/mochi-world.8 $(rpmbuild_aarch64)/SOURCES/
	cp install/etc/mochi/world.conf $(rpmbuild_aarch64)/SOURCES/
	cp install/etc/systemd/system/mochi-world.service $(rpmbuild_aarch64)/SOURCES/
	rpmbuild -bb --define "_topdir $(rpmbuild_aarch64)" --define "_version $(version)" --target aarch64 build/rpm/mochi-world.spec
	cp $(rpmbuild_aarch64)/RPMS/aarch64/mochi-world-$(version)-1.aarch64.rpm $(rpm_aarch64)
	rm -rf $(rpmbuild_aarch64)
	ls -l $(rpm_aarch64)

rpm-aarch64: $(rpm_aarch64)

$(rpm_armv7hl): $(bin)/mochi-world-linux-arm $(bin)/mochi-world.8
	rm -rf $(rpmbuild_armv7hl)
	mkdir -p $(rpmbuild_armv7hl)/SOURCES $(rpmbuild_armv7hl)/SPECS $(rpmbuild_armv7hl)/BUILD $(rpmbuild_armv7hl)/RPMS $(rpmbuild_armv7hl)/SRPMS
	cp $(bin)/mochi-world-linux-arm $(rpmbuild_armv7hl)/SOURCES/mochi-world
	cp $(bin)/mochi-world.8 $(rpmbuild_armv7hl)/SOURCES/
	cp install/etc/mochi/world.conf $(rpmbuild_armv7hl)/SOURCES/
	cp install/etc/systemd/system/mochi-world.service $(rpmbuild_armv7hl)/SOURCES/
	rpmbuild -bb --define "_topdir $(rpmbuild_armv7hl)" --define "_version $(version)" --target armv7hl build/rpm/mochi-world.spec
	cp $(rpmbuild_armv7hl)/RPMS/armv7hl/mochi-world-$(version)-1.armv7hl.rpm $(rpm_armv7hl)
	rm -rf $(rpmbuild_armv7hl)
	ls -l $(rpm_armv7hl)

rpm-armv7hl: $(rpm_armv7hl)

rpm: rpm-x86_64 rpm-aarch64 rpm-armv7hl

# --------------------------------------------------------------------------
# Windows MSI (requires wixl from msitools on Linux)
# --------------------------------------------------------------------------

$(msi): $(bin)/mochi-world.exe
	mkdir -p $(build_windows)
	cp $(bin)/mochi-world.exe $(build_windows)/
	cp build/msi/world.conf $(build_windows)/
	wixl -v --ext ui -a x64 -D Version=$(version) -D SourceDir=$(build_windows) -o $(msi) build/msi/world.wxs
	rm -rf $(build_windows)
	ls -l $(msi)

msi: $(msi)

# --------------------------------------------------------------------------
# macOS .pkg installers (requires bomutils at /opt/bomutils, xar)
# --------------------------------------------------------------------------

$(pkg_amd64): $(bin)/mochi-world-darwin-amd64
	PATH="/opt/bomutils/bin:$$PATH" ./build/scripts/build-pkg $(bin)/mochi-world-darwin-amd64 $(version) amd64 $(pkg_amd64)

$(pkg_arm64): $(bin)/mochi-world-darwin-arm64
	PATH="/opt/bomutils/bin:$$PATH" ./build/scripts/build-pkg $(bin)/mochi-world-darwin-arm64 $(version) arm64 $(pkg_arm64)

pkg-amd64: $(pkg_amd64)

pkg-arm64: $(pkg_arm64)

pkg: pkg-amd64 pkg-arm64

# --------------------------------------------------------------------------
# Docker
# --------------------------------------------------------------------------

# The image reuses the plain linux binaries — world has no platform-specific
# build flag (and no self-update poller) yet.
docker-stage: $(bin)/mochi-world $(bin)/mochi-world-linux-arm64
	rm -rf build/docker/bin
	mkdir -p build/docker/bin
	cp $(bin)/mochi-world build/docker/bin/mochi-world-amd64
	cp $(bin)/mochi-world-linux-arm64 build/docker/bin/mochi-world-arm64

# Build for the host arch only — fast iteration during development. Tags as
# :dev so it can't be confused with a real release.
docker-local: docker-stage
	docker build -t $(docker_image):dev .

docker: docker-stage
	@t=$$(date +%s); docker buildx build \
	    --platform linux/amd64,linux/arm64 \
	    --sbom=false --provenance=false \
	    --tag $(docker_image):$(version) \
	    --tag $(docker_image):$(major) \
	    --tag $(docker_image):latest \
	    --tag $(docker_image):production \
	    --push \
	    . && echo ">>> docker build+push: $$(($$(date +%s)-t))s" | tee -a $(timing)

# --------------------------------------------------------------------------
# Release
# --------------------------------------------------------------------------

release:
	@: > $(timing)
	@trap '$(MAKE) release-clean' EXIT; \
	t=$$(date +%s); $(MAKE) clean release-clean || exit 1; echo ">>> phase clean: $$(($$(date +%s)-t))s" | tee -a $(timing); \
	t=$$(date +%s); $(MAKE) -j$(JOBS) release-build || exit 1; echo ">>> phase build (incl docker push): $$(($$(date +%s)-t))s" | tee -a $(timing); \
	t=$$(date +%s); $(MAKE) release-publish || exit 1; echo ">>> phase publish (reindex + rsync): $$(($$(date +%s)-t))s" | tee -a $(timing); \
	echo; echo "=== release $(version) timing summary ==="; cat $(timing)

# Remove the release temporaries from /tmp: staging trees, rpmbuild trees, and
# the packaged artefacts, which release-publish only copies into ../packages.
# Every path is versioned, so this scrubs all versions, not just the current
# one. The patterns are disjoint from core's /tmp/mochi-server* — the two
# releases never clean each other's staging. $(timing) is deliberately kept:
# fixed name, a few hundred bytes, and the post-release report reads it after
# this target has run.
release-clean:
	-rm -rf /tmp/mochi-world_* /tmp/mochi-world-*.rpm /tmp/mochi-world-rpmbuild-*

# Parallel-safe build of every release artefact — same shape as core's: each
# branch stages in its own /tmp tree, so -j fans them across cores.
release-build: deb rpm msi pkg docker

# Publish into the shared packages tree beside core's artefacts. The apt/rpm
# indexers are core's — they scan the whole pool, so mochi-world joins the
# same signed repos. Only packages/world/versions.json is written; the
# per-channel versions.json files are mochi-server's. Never run a core and a
# world release concurrently: both rsync --delete the same tree (the local
# ../packages is canonical, so sequential runs are always safe).
release-publish:
	git tag -fa $(version) -m "$(version)"
	rm -f ../packages/apt/pool/main/mochi-world_*.deb
	cp $(deb_amd64) $(deb_arm64) $(deb_armhf) ../packages/apt/pool/main
	@t=$$(date +%s); ../core/build/scripts/apt-repository-update ../packages/apt `cat ../core/local/gpg.txt | tr -d '\n'` && echo ">>> apt reindex (scan + gpg sign): $$(($$(date +%s)-t))s" | tee -a $(timing)
	rm -f ../packages/rpm/Packages/mochi-world-*.rpm
	cp $(rpm_x86_64) $(rpm_aarch64) $(rpm_armv7hl) ../packages/rpm/Packages
	@t=$$(date +%s); ../core/build/scripts/rpm-repository-update ../packages/rpm && echo ">>> rpm reindex (createrepo): $$(($$(date +%s)-t))s" | tee -a $(timing)
	cp $(msi) ../packages/windows/mochi-world.msi
	cp $(pkg_amd64) ../packages/macos/mochi-world-amd64.pkg
	cp $(pkg_arm64) ../packages/macos/mochi-world-arm64.pkg
	mkdir -p ../packages/world
	echo '{"tracks": {"production": "$(version)"}}' > ../packages/world/versions.json
	# Publish to yuzu by name (not the packages.mochi-os.org alias) so the
	# target is deterministic regardless of where that record points.
	@t0=$$(date +%s); \
	rsync -av --delete ../packages/ root@yuzu.mochi-os.org:/srv/packages/ || exit 1; \
	echo ">>> rsync local->yuzu: $$(($$(date +%s)-t0))s" | tee -a $(timing)

# Install the published version on yuzu (verified). Separate from `release`
# (which only publishes packages) so deploying stays an explicit step. Pass
# apt flags via `make deploy DEPLOY_FLAGS=--reinstall` to redeploy an
# identical version.
deploy:
	./build/scripts/deploy $(DEPLOY_FLAGS)

.PHONY: all run1 test test-wasm wasm clean deb deb-amd64 deb-arm64 deb-armhf rpm rpm-x86_64 rpm-aarch64 rpm-armv7hl msi pkg pkg-amd64 pkg-arm64 docker docker-stage docker-local release release-clean release-build release-publish deploy
