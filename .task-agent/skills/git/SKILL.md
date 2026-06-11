---
name: git
description: Git workflow and commit conventions
---

# Git Workflow

## Commit Message Format

Use conventional commits: `type(scope): description`

Types: feat, fix, docs, style, refactor, test, chore

## Branch Naming

- Feature: `feat/<description>`
- Fix: `fix/<description>`
- Refactor: `refactor/<description>`

## Before Committing

1. Run `go test ./...` and ensure all pass
2. Run `go build ./...` and ensure no errors
3. Write a meaningful commit message
