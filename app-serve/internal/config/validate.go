// Package config builds the struct validator used by Load.
//
// @file      internal/config/validate.go
// @for       Validator construction that names the env key in a boot error.
// @uses      reflect, go-playground/validator
// @reason    A failed boot must name the environment variable an operator has to set.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     config
// @stability stable
// @since     2026-10-17
package config

import (
	"reflect"

	"github.com/go-playground/validator/v10"
)

// newValidator returns a validator whose field errors use the env struct tag,
// so a missing required variable is named by its key (GOOGLE_CLIENT_ID) rather
// than by its Go field.
func newValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		return field.Tag.Get("env")
	})
	return v
}
