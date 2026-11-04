package tlsfingerprint

// ChromeClientProfile returns a ClientHello profile that matches a desktop
// Chrome build closely enough for Cloudflare-fronted endpoints that score TLS
// fingerprints (accounts.x.ai, auth.x.ai). Values follow Chrome's ordering:
// GREASE at both ends, EMS/renegotiation before the parameterised extensions,
// and key_share / supported_versions in Chrome's tail order.
func ChromeClientProfile() *Profile {
	return &Profile{
		Name:         "Chrome Desktop",
		CipherSuites: []uint16{0x1301, 0x1302, 0x1303, 0xc02b, 0xc02f, 0xc02c, 0xc030, 0xcca9, 0xcca8, 0xc013, 0xc014, 0x009c, 0x009d, 0x002f, 0x0035},
		Curves:       []uint16{0x001d, 0x0017, 0x0018},
		PointFormats: []uint16{0},
		SignatureAlgorithms: []uint16{
			0x0403, 0x0804, 0x0401, 0x0503, 0x0805, 0x0501, 0x0806, 0x0601, 0x0802, 0x0603, 0x0803,
		},
		EnableGREASE:      true,
		ALPNProtocols:     []string{"http/1.1"},
		SupportedVersions: []uint16{0x0304, 0x0303},
		KeyShareGroups:    []uint16{0x001d},
		PSKModes:          []uint16{1},
		// Chrome also sends compress_certificate(27), application_settings(17513)
		// and padding(21); this builder would emit them with empty payloads,
		// which the server rejects as "error decoding message", so they are
		// omitted. Cipher suite order, curves and the remaining extension order
		// still match Chrome's JA3 shape.
		Extensions: []uint16{0x0a0a, 0, 23, 65281, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43, 0x1a1a},
	}
}
