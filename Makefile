.PHONY: build build-cli build-scenarios clean

build: build-cli

build-cli:
	cd cli && go build -o ../bin/lightnet .

clean:
	rm -rf bin/
	cd scenarios && cargo clean
