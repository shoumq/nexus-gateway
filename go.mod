module nexus-gateway

go 1.25.0

require (
	google.golang.org/grpc v1.66.1
	google.golang.org/protobuf v1.34.2

	// local proto module
	nexus v0.0.0
)

require (
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.33.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240604185151-ef581f913117 // indirect
)

replace nexus => ../nexus-ai
