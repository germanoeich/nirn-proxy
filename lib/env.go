package lib

import (
	"os"
	"strconv"
)

func EnvGet(name string, defaultVal string) string {
	val := os.Getenv(name)
	if val == "" {
		return defaultVal
	}
	return val
}

func EnvGetBool(name string, defaultVal bool) bool {
	val := os.Getenv(name)
	if val == "" {
		return defaultVal
	}

	if val != "true" && val != "false" {
		panic("Invalid env var, expected true or false, got " + val + " for " + name)
	}
	return val == "true"
}


func EnvMustGet(name string) string {
	val := os.Getenv(name)
	if val == "" {
		panic("ENV var " + name + " is empty")
	}
	return val
}

func EnvGetInt(name string, defaultVal int) int {
	val := os.Getenv(name)
	if val == "" {
		return defaultVal
	}

	valParsed, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		panic("Failed to parse " + name)
	}
	return int(valParsed)
}