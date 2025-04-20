# Contributing to nextr4y

Thank you for your interest in contributing to nextr4y! This document provides guidelines and instructions to help you get started.

## How to Contribute

### Reporting Bugs

If you encounter a bug, please file an issue using the bug report template. Include as much detail as possible:

- A clear description of the problem
- Steps to reproduce the issue
- Expected behavior vs. actual behavior
- Screenshots if applicable
- Your environment (OS, Go version, etc.)

### Suggesting Enhancements

We welcome feature requests and enhancements! Please file an issue using the feature request template and include:

- A clear description of the feature/enhancement
- Why it would be useful
- Any implementation ideas you may have

### Pull Requests

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Run tests to ensure your changes don't break existing functionality
5. Commit your changes (`git commit -m 'Add some amazing feature'`)
6. Push to the branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

### Pull Request Guidelines

- Keep your changes focused. If you want to fix multiple issues, submit separate pull requests.
- Write clean, maintainable code
- Include comments where necessary
- Add or update tests as needed
- Update documentation to reflect your changes
- Follow existing code style and formatting
- Make sure all tests pass before submitting

## Development Setup

### Prerequisites

- Go 1.24
- Git

### Local Development

```bash
# Clone your fork
git clone https://github.com/your-username/nextr4y.git
cd nextr4y

# Add the original repository as a remote
git remote add upstream https://github.com/rodrigopv/nextr4y.git

# Build the application
go build -o nextr4y ./cmd/nextr4y

# Run tests
go test ./...
```

## Style Guidelines

### Go Code

- Follow standard Go formatting (use `gofmt` or `go fmt`)
- Follow the [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Document exported functions, types, and variables
- Aim for clear, readable code over clever solutions

### Git Commit Messages

- Use the present tense ("Add feature" not "Added feature")
- Use the imperative mood ("Move cursor to..." not "Moves cursor to...")
- Limit the first line to 72 characters or less
- Reference issues and pull requests after the first line

## Additional Resources

- [Go Documentation](https://golang.org/doc/)
- [Effective Go](https://golang.org/doc/effective_go.html)
- [Next.js Documentation](https://nextjs.org/docs) (for understanding the target technology)

## License

By contributing to NextR4y, you agree that your contributions will be licensed under the project's [MIT License](LICENSE). 
