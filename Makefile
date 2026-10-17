.PHONY: setup proto tidy frontend test vet

# Первый запуск: сгенерировать gRPC-код и подтянуть зависимости (нужны protoc и доступ к Go proxy).
setup: proto tidy frontend

proto:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	mkdir -p internal/authpb
	protoc -I proto \
	  --go_out=. --go_opt=module=github.com/compico/tw-srv \
	  --go-grpc_out=. --go-grpc_opt=module=github.com/compico/tw-srv \
	  proto/auth/v1/auth.proto

tidy:
	go mod tidy

frontend:
	cd frontend && npm ci && npm run build

test:
	go test ./...

vet:
	go vet ./...
