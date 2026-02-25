binary := "soak"

# build the binary
build:
    go build -o {{binary}} .

# build and run the TUI board
run: build
    ./{{binary}}

# install soak to ~/.soak/
install: build
    mkdir -p ~/.soak
    cp {{binary}} ~/.soak/soak
    codesign --force --deep --sign - ~/.soak/soak
    xattr -d com.apple.provenance ~/.soak/soak 2>/dev/null || true
    rm {{binary}}
    @echo "Installed to ~/.soak/soak (signed and quarantine removed)"
    @echo 'Add ~/.soak to your PATH: export PATH="$HOME/.soak:$PATH"'

# remove build artifacts
clean:
    rm -f {{binary}}

# kill processes, wipe NATS data, tear down tmux — full reset
nuke:
    bash dev_utils/clean.sh

# reset state then start fresh
fresh: nuke build run

# run go vet
vet:
    go vet ./...
