// crypto 命令：RSA 密钥生成 / 加密 / 解密（Go 标准库，零依赖）。
//
// 设计决策（对比你在 JS 里被迫选 jsencrypt/jsrsasign 的场景）：
//   - 填充方案：OAEP-SHA256（现代推荐，比 jsencrypt 默认的 PKCS#1 v1.5 更安全）
//   - 大数据支持：自动混合加密（RSA 保护 AES-256-GCM 会话密钥 → AES 加密任意大小数据）
//     （jsencrypt 只能加密 ≤ key_size/8-42 字节，超长直接失败）
//   - 密钥格式：PEM 输出（可读）；输入自动识别 PEM / Base64 编码 PEM
//   - 密钥位数：默认 2048（安全与速度平衡），支持 1024/2048/4096
//
// 用法：
//
//	zpack crypto genkey [--bits 2048]                    → 生成密钥对（JSON 输出）
//	zpack crypto encrypt --key-file pub.pem "文本"        → 公钥加密（Base64 输出）
//	zpack crypto encrypt --key-file pub.pem --file data.bin
//	zpack crypto decrypt --key-file priv.pem "Base64密文" → 私钥解密
package cli

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// CRYPTO_DEFAULT_BITS 默认密钥位数（2048 = 安全与速度平衡点）
const CRYPTO_DEFAULT_BITS = 2048

// CRYPTO_HYBRID_MAGIC 混合加密魔数前缀（区分直接 RSA vs 混合 RSA+AES）
const CRYPTO_HYBRID_MAGIC = "ZPACK:"

// NewCryptoCmd crypto 命令组
func NewCryptoCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "crypto <genkey|encrypt|decrypt> [参数]",
		Short:              "RSA 密钥生成 / 公钥加密 / 私钥解密（混合加密支持任意大小数据）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runCrypto) },
	}
}

func runCrypto(args []string) {
	subcommand := cmdutil.FirstPositional(args)
	restArgs := cryptoRestArgs(args)
	switch subcommand {
	case "genkey":
		cryptoGenKey(restArgs)
	case "encrypt":
		cryptoEncrypt(restArgs)
	case "decrypt":
		cryptoDecrypt(restArgs)
	default:
		stdout.ErrorPrint("用法: crypto genkey [--bits 2048] 或 crypto encrypt --key-file pub.pem <文本> 或 crypto decrypt --key-file priv.pem <密文>")
	}
}

// ─── 密钥生成 ───

func cryptoGenKey(restArgs []string) {
	bits := CRYPTO_DEFAULT_BITS
	if bitsText, hasBits := cmdutil.FlagValue(restArgs, "--bits"); hasBits {
		var parseOk bool
		bits, parseOk = parseIntStrict(bitsText)
		if !parseOk || bits < 512 || bits > 4096 || bits%256 != 0 {
			stdout.ErrorPrint(fmt.Sprintf("--bits 需要 512-4096 的 256 倍数整数，收到: %s", bitsText))
		}
	}
	privateKey, genErr := rsa.GenerateKey(rand.Reader, bits)
	if genErr != nil {
		stdout.ErrorPrint("密钥生成失败: " + genErr.Error())
	}
	// PEM 编码
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: marshalPublicKey(&privateKey.PublicKey),
	})
	// --output 前缀：密钥落盘（私钥 0600 保护 / 公钥 0644 共享）
	if outputPrefix, hasOutput := cmdutil.FlagValue(restArgs, "--output"); hasOutput {
		privPath := outputPrefix + "_private.pem"
		pubPath := outputPrefix + "_public.pem"
		if writeErr := os.WriteFile(privPath, privPEM, 0o600); writeErr != nil {
			stdout.ErrorPrint("无法写入私钥文件: " + writeErr.Error())
		}
		if writeErr := os.WriteFile(pubPath, pubPEM, 0o644); writeErr != nil {
			stdout.ErrorPrint("无法写入公钥文件: " + writeErr.Error())
		}
		stdout.JSONPrint(genkeyFilesJSON{Bits: bits, PublicKey: pubPath, PrivateKey: privPath})
		return
	}
	stdout.JSONPrint(genkeyInlineJSON{
		Bits: bits, PublicKey: string(pubPEM), PrivateKey: string(privPEM),
		PublicB64:  base64.StdEncoding.EncodeToString(pubPEM),
		PrivateB64: base64.StdEncoding.EncodeToString(privPEM),
	})
}

// marshalPublicKey 公钥序列化（PKIX 格式，最通用）
func marshalPublicKey(pub *rsa.PublicKey) []byte {
	der, _ := x509.MarshalPKIXPublicKey(pub)
	return der
}

// ─── 加密 ───

func cryptoEncrypt(restArgs []string) {
	keyContent := cryptoResolveKey(restArgs)
	if keyContent == nil {
		stdout.ErrorPrint("需要公钥：--key <PEM文本> 或 --key-file <路径>")
	}
	publicKey, keyErr := parsePublicKey(string(keyContent))
	if keyErr != nil {
		stdout.ErrorPrint("无法解析公钥: " + keyErr.Error())
	}
	data := cryptoResolveData(restArgs)

	ciphertext, encryptErr := rsaEncrypt(publicKey, data)
	if encryptErr != nil {
		stdout.ErrorPrint("加密失败: " + encryptErr.Error())
	}
	encoded := base64.StdEncoding.EncodeToString(ciphertext)
	outputPath := cryptoResolveOutput(restArgs)
	if outputPath != "" {
		if writeErr := os.WriteFile(outputPath, []byte(encoded), 0o644); writeErr != nil {
			stdout.ErrorPrint("无法写入文件: " + writeErr.Error())
		}
		stdout.JSONPrint(fileOutputJSON{Output: outputPath, SizeBytes: len(encoded)})
		return
	}
	stdout.TextPrint(encoded)
}

// rsaEncrypt 混合加密：短数据直接 RSA-OAEP；长数据自动 RSA+AES-256-GCM
func rsaEncrypt(pub *rsa.PublicKey, plaintext []byte) ([]byte, error) {
	maxDirect := pub.Size() - 2*sha256.Size - 2 // OAEP-SHA256 最大明文
	if len(plaintext) <= maxDirect {
		// 直接 RSA
		return rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, plaintext, nil)
	}
	// 混合：RSA 加密 AES 会话密钥 → AES-GCM 加密数据
	sessionKey := make([]byte, 32) // AES-256
	if _, err := rand.Read(sessionKey); err != nil {
		return nil, err
	}
	encryptedKey, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, sessionKey, nil)
	if err != nil {
		return nil, err
	}
	aesBlock, _ := aes.NewCipher(sessionKey)
	aesGCM, _ := cipher.NewGCM(aesBlock)
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	encryptedData := aesGCM.Seal(nonce, nonce, plaintext, nil)
	// 格式：magic + key_len(2B) + encrypted_key + encrypted_data
	result := make([]byte, 0, len(CRYPTO_HYBRID_MAGIC)+2+len(encryptedKey)+len(encryptedData))
	result = append(result, CRYPTO_HYBRID_MAGIC...)
	result = append(result, byte(len(encryptedKey)>>8), byte(len(encryptedKey)))
	result = append(result, encryptedKey...)
	result = append(result, encryptedData...)
	return result, nil
}

// ─── 解密 ───

func cryptoDecrypt(restArgs []string) {
	keyContent := cryptoResolveKey(restArgs)
	if keyContent == nil {
		stdout.ErrorPrint("需要私钥：--key <PEM文本> 或 --key-file <路径>")
	}
	privateKey, keyErr := parsePrivateKey(string(keyContent))
	if keyErr != nil {
		stdout.ErrorPrint("无法解析私钥: " + keyErr.Error())
	}
	data := cryptoResolveData(restArgs)
	ciphertext, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if decodeErr != nil {
		stdout.ErrorPrint("无效的 Base64 密文: " + decodeErr.Error())
	}
	plaintext, decryptErr := rsaDecrypt(privateKey, ciphertext)
	if decryptErr != nil {
		stdout.ErrorPrint("解密失败（密钥不匹配或数据损坏）: " + decryptErr.Error())
	}
	outputPath := cryptoResolveOutput(restArgs)
	if outputPath != "" {
		if writeErr := os.WriteFile(outputPath, plaintext, 0o644); writeErr != nil {
			stdout.ErrorPrint("无法写入文件: " + writeErr.Error())
		}
		stdout.JSONPrint(fileOutputJSON{Output: outputPath, SizeBytes: len(plaintext)})
		return
	}
	fmt.Fprintf(os.Stdout, "%s", plaintext)
	os.Exit(0)
}

// rsaDecrypt 混合解密：自动识别直接 RSA vs 混合 RSA+AES
func rsaDecrypt(priv *rsa.PrivateKey, ciphertext []byte) ([]byte, error) {
	// 检查混合加密魔数
	if len(ciphertext) > len(CRYPTO_HYBRID_MAGIC) && string(ciphertext[:len(CRYPTO_HYBRID_MAGIC)]) == CRYPTO_HYBRID_MAGIC {
		return rsaDecryptHybrid(priv, ciphertext[len(CRYPTO_HYBRID_MAGIC):])
	}
	// 直接 RSA
	return rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, ciphertext, nil)
}

func rsaDecryptHybrid(priv *rsa.PrivateKey, data []byte) ([]byte, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("混合加密数据过短")
	}
	keyLen := int(data[0])<<8 | int(data[1])
	if len(data) < 2+keyLen {
		return nil, fmt.Errorf("混合加密数据不完整")
	}
	encryptedKey := data[2 : 2+keyLen]
	encryptedData := data[2+keyLen:]
	sessionKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, encryptedKey, nil)
	if err != nil {
		return nil, err
	}
	aesBlock, _ := aes.NewCipher(sessionKey)
	aesGCM, _ := cipher.NewGCM(aesBlock)
	nonceSize := aesGCM.NonceSize()
	if len(encryptedData) < nonceSize {
		return nil, fmt.Errorf("AES 数据过短")
	}
	nonce, encryptedBody := encryptedData[:nonceSize], encryptedData[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, encryptedBody, nil)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

// ─── 密钥解析 ───

// parsePublicKey 解析公钥：自动识别 PEM 格式 / Base64 编码 PEM
func parsePublicKey(keyText string) (*rsa.PublicKey, error) {
	keyText = strings.TrimSpace(keyText)
	// 如果已是 PEM 格式
	if strings.Contains(keyText, "-----BEGIN") {
		block, _ := pem.Decode([]byte(keyText))
		if block == nil {
			return nil, fmt.Errorf("无效的 PEM 格式")
		}
		return parsePublicDER(block.Bytes)
	}
	// 尝试 Base64 解码出 PEM（strip 换行/空白——macOS base64 命令是多行输出）
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, keyText)
	decoded, decodeErr := base64.StdEncoding.DecodeString(cleaned)
	if decodeErr != nil {
		return nil, fmt.Errorf("无法识别的公钥格式（需要 PEM 或 Base64 编码 PEM）")
	}
	block, _ := pem.Decode(decoded)
	if block == nil {
		return nil, fmt.Errorf("Base64 解码后不是有效的 PEM")
	}
	return parsePublicDER(block.Bytes)
}

func parsePublicDER(der []byte) (*rsa.PublicKey, error) {
	// 尝试 PKIX 格式（最通用）
	if pub, err := x509.ParsePKIXPublicKey(der); err == nil {
		if rsaPub, ok := pub.(*rsa.PublicKey); ok {
			return rsaPub, nil
		}
		return nil, fmt.Errorf("不是 RSA 公钥")
	}
	// 尝试 PKCS1 格式
	if pub, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return pub, nil
	}
	return nil, fmt.Errorf("无法解析公钥 DER 数据")
}

// parsePrivateKey 解析私钥：自动识别 PEM / Base64 编码 PEM
func parsePrivateKey(keyText string) (*rsa.PrivateKey, error) {
	keyText = strings.TrimSpace(keyText)
	if strings.Contains(keyText, "-----BEGIN") {
		block, _ := pem.Decode([]byte(keyText))
		if block == nil {
			return nil, fmt.Errorf("无效的 PEM 格式")
		}
		return parsePrivateDER(block.Bytes)
	}
	cleanedPriv := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, keyText)
	decoded, decodeErr := base64.StdEncoding.DecodeString(cleanedPriv)
	if decodeErr != nil {
		return nil, fmt.Errorf("无法识别的私钥格式")
	}
	block, _ := pem.Decode(decoded)
	if block == nil {
		return nil, fmt.Errorf("Base64 解码后不是有效的 PEM")
	}
	return parsePrivateDER(block.Bytes)
}

func parsePrivateDER(der []byte) (*rsa.PrivateKey, error) {
	// PKCS1（传统 RSA 私钥格式）
	if priv, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return priv, nil
	}
	// PKCS8（通用私钥格式）
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if priv, ok := key.(*rsa.PrivateKey); ok {
			return priv, nil
		}
		return nil, fmt.Errorf("不是 RSA 私钥")
	}
	return nil, fmt.Errorf("无法解析私钥 DER 数据")
}

// ─── 输入输出辅助 ───

// cryptoResolveKey 解析密钥输入：--key <文本> 或 --key-file <路径>
func cryptoResolveKey(restArgs []string) []byte {
	for argIndex := 0; argIndex < len(restArgs); argIndex++ {
		switch {
		case restArgs[argIndex] == "--key":
			if argIndex+1 >= len(restArgs) {
				stdout.ErrorPrint("--key 缺少值")
			}
			return []byte(restArgs[argIndex+1])
		case restArgs[argIndex] == "--key-file":
			if argIndex+1 >= len(restArgs) {
				stdout.ErrorPrint("--key-file 缺少值")
			}
			data, readErr := os.ReadFile(restArgs[argIndex+1])
			if readErr != nil {
				stdout.ErrorPrint("无法读取密钥文件: " + readErr.Error())
			}
			return data
		}
	}
	return nil
}

// cryptoResolveData 解析数据输入：位置参数 或 --file <路径> 或 stdin
func cryptoResolveData(restArgs []string) []byte {
	for argIndex := 0; argIndex < len(restArgs); argIndex++ {
		if restArgs[argIndex] == "--file" {
			if argIndex+1 >= len(restArgs) {
				stdout.ErrorPrint("--file 缺少值")
			}
			data, readErr := os.ReadFile(restArgs[argIndex+1])
			if readErr != nil {
				stdout.ErrorPrint("无法读取文件: " + readErr.Error())
			}
			return data
		}
	}
	// 位置参数（跳过所有旗标及其值）
	valueFlags := map[string]bool{"--key": true, "--key-file": true, "--output": true, "--bits": true, "--file": true}
	skipNext := false
	for _, arg := range restArgs {
		if skipNext {
			skipNext = false
			continue
		}
		if valueFlags[arg] {
			skipNext = true
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return []byte(arg)
		}
	}
	// stdin
	data, _ := io.ReadAll(os.Stdin)
	return data
}

func cryptoResolveOutput(restArgs []string) string {
	for argIndex := 0; argIndex < len(restArgs); argIndex++ {
		if restArgs[argIndex] == "--output" && argIndex+1 < len(restArgs) {
			return restArgs[argIndex+1]
		}
	}
	return ""
}

func cryptoRestArgs(args []string) []string {
	for argIndex, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			rest := append([]string{}, args[:argIndex]...)
			return append(rest, args[argIndex+1:]...)
		}
	}
	return args
}

// parseIntStrict 本地版本（避免 cmdutil 的 import cycle）
func parseIntStrict(text string) (int, bool) {
	var value int
	if _, err := fmt.Sscanf(text, "%d", &value); err != nil {
		return 0, false
	}
	return value, true
}

// ── crypto 输出契约 struct ──

type genkeyFilesJSON struct {
	Bits       int    `json:"bits"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

type genkeyInlineJSON struct {
	Bits       int    `json:"bits"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	PublicB64  string `json:"public_b64"`
	PrivateB64 string `json:"private_b64"`
}
