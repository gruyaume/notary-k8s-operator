# Contributing

## Testing

This project uses standard Go testing tools:

```shell
go test ./...             # Run all tests
go vet ./...              # Check for suspicious constructs
golangci-lint run ./...   # Check for linting issues
```

## Build the charm

Build the charm in this git repository using:

```shell
charmcraft pack
```
