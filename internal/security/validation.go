package security

import (
	"errors"
	"regexp"
	"strings"
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func ValidateEmail(email string) error {
	email = strings.TrimSpace(email)
	if len(email) > 320 || !emailPattern.MatchString(email) {
		return errors.New("invalid email")
	}
	return nil
}

func ValidateIdentifier(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return errors.New("identifier must be between 1 and 64 bytes")
	}
	return nil
}
