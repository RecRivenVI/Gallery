package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidateInputsRejectsInvalidIdentityAndSchema(t *testing.T) {
	dir := t.TempDir()
	historical := filepath.Join(dir, "historical.exe")
	current := filepath.Join(dir, "current.exe")
	for _, path := range []string{historical, current} {
		if err := os.WriteFile(path, []byte("binary"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	validHistorical := "0123456789abcdef0123456789abcdef01234567"
	validCurrent := "89abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name                            string
		historicalBin, currentBin       string
		historicalCommit, currentCommit string
		historicalSchema, currentSchema int64
	}{
		{name: "same binary", historicalBin: historical, currentBin: historical, historicalCommit: validHistorical, currentCommit: validCurrent, historicalSchema: 23, currentSchema: 24},
		{name: "short commit", historicalBin: historical, currentBin: current, historicalCommit: "0123", currentCommit: validCurrent, historicalSchema: 23, currentSchema: 24},
		{name: "uppercase commit", historicalBin: historical, currentBin: current, historicalCommit: validHistorical, currentCommit: "89ABCDEF0123456789ABCDEF0123456789ABCDEF", historicalSchema: 23, currentSchema: 24},
		{name: "same commit", historicalBin: historical, currentBin: current, historicalCommit: validHistorical, currentCommit: validHistorical, historicalSchema: 23, currentSchema: 24},
		{name: "non increasing schema", historicalBin: historical, currentBin: current, historicalCommit: validHistorical, currentCommit: validCurrent, historicalSchema: 24, currentSchema: 24},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := validateInputs(test.historicalBin, test.currentBin, test.historicalCommit, test.currentCommit, test.historicalSchema, test.currentSchema); err == nil {
				t.Fatal("预期拒绝非法输入")
			}
		})
	}
}

func TestSealControlDatabaseIsDeterministicAndSensitive(t *testing.T) {
	appRoot := t.TempDir()
	data := filepath.Join(appRoot, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(data, "control.db")
	if err := os.WriteFile(control, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := sealControlDatabase(appRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sealControlDatabase(appRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("相同 control 数据库封印不稳定")
	}
	if err := os.WriteFile(control, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := sealControlDatabase(appRoot)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(first, changed) {
		t.Fatal("control 数据库变化没有改变封印")
	}
}

func TestAssertDowngradeLogRequiresExactFutureMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downgrade.log")
	if err := os.WriteFile(path, []byte("数据库包含当前程序未知的 migration version 24"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := assertDowngradeLog(path, 24); err != nil {
		t.Fatal(err)
	}
	if err := assertDowngradeLog(path, 25); err == nil {
		t.Fatal("错误的新 schema 版本不应通过")
	}
}
