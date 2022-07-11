package util

import (
	"os"
	"strconv"
	"testing"
)

func TestEnvGetWorks(t *testing.T) {
	defer os.Unsetenv("TEST_ENV_VAR")
	os.Setenv("TEST_ENV_VAR", "test")
	val := EnvGet("TEST_ENV_VAR", "default")
	if val != "test" {
		t.Error("Expected test, got " + val)
	}
}

func TestEnvGetDefaultWorks(t *testing.T) {
	val := EnvGet("TEST_ENV_VAR", "default")
	if val != "default" {
		t.Error("Expected default, got " + val)
	}
}

func TestEnvGetBool(t *testing.T) {
	defer os.Unsetenv("TEST_ENV_VAR")
	os.Setenv("TEST_ENV_VAR", "true")
	val := EnvGetBool("TEST_ENV_VAR", false)
	if val != true {
		t.Error("Expected true, got " + strconv.FormatBool(val))
	}
}

func TestEnvGetBoolDefaultWorks(t *testing.T) {
	val := EnvGetBool("TEST_ENV_VAR", true)
	if val != true {
		t.Error("Expected false, got " + strconv.FormatBool(val))
	}
}

func TestEnvGetBoolPanicsOnInvalidValue(t *testing.T) {
	defer os.Unsetenv("TEST_ENV_VAR")
	os.Setenv("TEST_ENV_VAR", "invalid")
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic, got none")
		}
	}()
	EnvGetBool("TEST_ENV_VAR", true)
}

func TestEnvMustGet(t *testing.T) {
	defer os.Unsetenv("TEST_ENV_VAR")
	os.Setenv("TEST_ENV_VAR", "test")
	val := EnvMustGet("TEST_ENV_VAR")
	if val != "test" {
		t.Error("Expected test, got " + val)
	}
}

func TestEnvMustGetPanicsWhenNoValue(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic, got none")
		}
	}()
	EnvMustGet("TEST_ENV_VAR")
}

func TestEnvGetInt(t *testing.T) {
	defer os.Unsetenv("TEST_ENV_VAR")
	os.Setenv("TEST_ENV_VAR", "1")
	val := EnvGetInt("TEST_ENV_VAR", 0)
	if val != 1 {
		t.Error("Expected 1, got " + strconv.Itoa(val))
	}
}

func TestEnvGetIntDefaultWorks(t *testing.T) {
	val := EnvGetInt("TEST_ENV_VAR", 1)
	if val != 1 {
		t.Error("Expected 1, got " + strconv.Itoa(val))
	}
}

func TestEnvGetIntPanicsOnValidValue(t *testing.T) {
	defer os.Unsetenv("TEST_ENV_VAR")
	os.Setenv("TEST_ENV_VAR", "invalid")
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic, got none")
		}
	}()
	EnvGetInt("TEST_ENV_VAR", 1)
}
