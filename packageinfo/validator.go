package packageinfo

import (
	"errors"
	"reflect"

	"github.com/go-playground/validator/v10"
)

const (
	ValidatorTagPackageVersion = "dspm_package_version"
	ValidatorTagPackageID      = "dspm_package_id"
	ValidatorTagGenericID      = "dspm_generic_id"
)

// RegisterValidator registers package info validators with go-playground/validator.
func RegisterValidator(v *validator.Validate) error {
	if v == nil {
		return errors.New("validator cannot be nil")
	}

	if err := v.RegisterValidation(ValidatorTagPackageVersion, func(field validator.FieldLevel) bool {
		return validatePackageVersionValue(field.Field())
	}); err != nil {
		return err
	}

	if err := v.RegisterValidation(ValidatorTagPackageID, func(field validator.FieldLevel) bool {
		return validateIdentifierValue(field.Field(), IsValidPackageIdentifier)
	}); err != nil {
		return err
	}

	return v.RegisterValidation(ValidatorTagGenericID, func(field validator.FieldLevel) bool {
		return validateIdentifierValue(field.Field(), IsValidGenericIdentifier)
	})
}

func validatePackageVersionValue(value reflect.Value) bool {
	if !value.IsValid() {
		return false
	}

	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return false
		}
		return validatePackageVersionValue(value.Elem())
	}

	switch value.Kind() {
	case reflect.String:
		return IsValidPackageVersion(value.String())
	case reflect.Struct:
		return value.Type() == reflect.TypeOf(PackageVersion{})
	default:
		return false
	}
}

func validateIdentifierValue(value reflect.Value, validate func(string) bool) bool {
	if !value.IsValid() {
		return false
	}

	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return false
		}
		return validateIdentifierValue(value.Elem(), validate)
	}

	if value.Kind() != reflect.String {
		return false
	}
	return validate(value.String())
}
