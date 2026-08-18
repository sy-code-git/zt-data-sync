package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}

func nowUnix() int64 { return time.Now().Unix() }

// generatePassword 随机密码生成（§9.1：crypto/rand，可配长度/字符集）。
func generatePassword(length int) (string, error) {
	if length < 4 || length > 128 {
		return "", fmt.Errorf("长度须在 4-128 之间")
	}
	charset := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for i, v := range b {
		out[i] = charset[int(v)%len(charset)]
	}
	return string(out), nil
}
