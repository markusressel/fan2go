BINARY_NAME=fan2go
OUTPUT_DIR=bin/

build:
	go build -o ${OUTPUT_DIR}${BINARY_NAME} main.go

run:
	go build -o ${OUTPUT_DIR}${BINARY_NAME} main.go
	./${OUTPUT_DIR}${BINARY_NAME}

test:
	sudo go test -v ./...

clean:
	go clean
	rm ${OUTPUT_DIR}${BINARY_NAME}