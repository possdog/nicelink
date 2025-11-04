# tllc

A little baby link shortener. Zero trackers.

## Usage
Use the web frontend at [t.l.lc](https://t.l.lc/), or make a POST request:
```
curl -X POST 'https://t.l.lc/?url=https://example.com/'
```

## Building
This was made on MacOS for deployment on a Debian server. To that end:
```
# 1. Add the 64-bit Debian target
rustup target add x86_64-unknown-linux-gnu

# 2. Install a Linux-aware linker (Homebrew)
brew install SergioBenitez/osxct/x86_64-unknown-linux-gnu

# 3. Execute the provided build script (build-linux.sh)
./build-linux.sh
```