package crypto

import (
	"encoding/asn1"
)

// buildSM2SPKI 手工构造 SM2 公钥 SPKI DER（用于测试畸形点）。
func buildSM2SPKI(bitString []byte) []byte {
	oidEC := asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidSM2 := asn1.ObjectIdentifier{1, 2, 156, 10197, 1, 301}

	type algID struct {
		Algorithm  asn1.ObjectIdentifier
		Parameters asn1.ObjectIdentifier
	}
	type spki struct {
		Algo      algID
		BitString asn1.BitString
	}
	der, _ := asn1.Marshal(spki{
		Algo:      algID{oidEC, oidSM2},
		BitString: asn1.BitString{Bytes: bitString, BitLength: len(bitString) * 8},
	})
	return der
}
