package proto

//go:generate protoc --proto_path=../. --go_out=../. --go_opt=paths=source_relative proto/bongo/v1/blob.proto proto/bongo/v1/command.proto proto/bongo/v1/mail.proto proto/bongo/v1/mailbox.proto proto/bongo/v1/profile.proto proto/bongo/v1/queue.proto
