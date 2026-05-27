package domain

import (
	"fmt"
	"strings"
)

type NotFoundError struct {
	Entity string
	ID     string
}

func (e *NotFoundError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("%s with ID '%s' not found", e.Entity, e.ID)
	}
	return fmt.Sprintf("%s not found", e.Entity)
}

type AlreadyExistsError struct {
	Entity string
	Field  string
	Value  string
}

func (e *AlreadyExistsError) Error() string {
	if e.Field != "" && e.Value != "" {
		return fmt.Sprintf("%s with %s '%s' already exists", e.Entity, e.Field, e.Value)
	}
	return fmt.Sprintf("%s already exists", e.Entity)
}

type ValidationError struct {
	Entity string
	Errors map[string]string
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("%s validation failed", e.Entity)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s validation failed: ", e.Entity)
	i := 0
	for field, msg := range e.Errors {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s: %s", field, msg)
		i++
	}
	return sb.String()
}

type UnauthorizedError struct{ Message string }

func (e *UnauthorizedError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "authentication required"
}

type ForbiddenError struct{ Message string }

func (e *ForbiddenError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "permission denied"
}
