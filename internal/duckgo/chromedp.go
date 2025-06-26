package duckgo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"

	"github.com/chromedp/chromedp"
)

// sha256AndBase64 使用 SHA-256 对字符串进行哈希，然后将结果进行 Base64 编码。
func sha256AndBase64(input string) string {
	h := sha256.New()
	h.Write([]byte(input))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ExecuteJS 在浏览器中执行JavaScript
func ExecuteJS(jsCode string) (map[string]interface{}, error) {
	// 构建优化的启动选项
	allocatorOptions := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Headless,
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-plugins", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("ignore-ssl-errors", true),
		chromedp.Flag("ignore-certificate-errors-spki-list", true),
		chromedp.WindowSize(1200, 800),
		chromedp.UserAgent(UA),
	}
	// 创建分配器上下文
	allocatorCtx, _ := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	// create context
	ctx, cancel := chromedp.NewContext(allocatorCtx)
	defer cancel()

	// run task
	var result map[string]interface{}
	err := chromedp.Run(ctx,
		chromedp.Navigate("about:blank"),
		chromedp.Evaluate(jsCode, &result),
	)
	if err != nil {
		return nil, fmt.Errorf("执行 JavaScript 失败: %w", err)
	}
	return result, nil
}

// ExecuteObfuscatedJs 执行经过Base64编码的混淆JavaScript
func ExecuteObfuscatedJs(base64EncodedJs string) (string, error) {
	// 解码Base64编码的JavaScript
	decodedJsBytes, err := base64.StdEncoding.DecodeString(base64EncodedJs)
	if err != nil {
		return "", fmt.Errorf("解码 Base64 JS 字符串失败: %w", err)
	}
	decodedJs := string(decodedJsBytes)
	log.Printf("开始执行JS")
	// 执行JavaScript
	rawJsResult, err := ExecuteJS(decodedJs)
	log.Printf("JS执行结束")
	if err != nil {
		return "", err
	}

	// 检查执行结果
	if rawJsResult == nil {
		return "", fmt.Errorf("JS 执行返回空结果")
	}
	if errMsg, ok := rawJsResult["error"].(string); ok && errMsg != "" {
		return "", fmt.Errorf("JS 执行报告错误: %s (详情: %v)", errMsg, rawJsResult["stack"])
	}

	// 后处理：对 client_hashes 进行 SHA-256 和 Base64 编码
	if clientHashesInterface, ok := rawJsResult["client_hashes"].([]interface{}); ok && len(clientHashesInterface) >= 2 {
		// 处理 client_hashes[0]
		if combinedUserAgentHash, ok := clientHashesInterface[0].(string); ok {
			rawJsResult["client_hashes"].([]interface{})[0] = sha256AndBase64(combinedUserAgentHash)
		} else {
			log.Printf("警告: client_hashes[0] 不是字符串或格式不正确，跳过处理。")
		}

		// 处理 client_hashes[1]
		if htmlParsingBasedHash, ok := clientHashesInterface[1].(string); ok {
			rawJsResult["client_hashes"].([]interface{})[1] = sha256AndBase64(htmlParsingBasedHash)
		} else {
			log.Printf("警告: client_hashes[1] 不是字符串或格式不正确，跳过处理。")
		}
	} else {
		log.Printf("警告: 未找到 client_hashes 数组或长度不足，跳过后处理。")
	}

	// 序列化结果为JSON
	finalJsonBytes, err := json.Marshal(rawJsResult)
	if err != nil {
		return "", fmt.Errorf("序列化最终 JSON 结果失败: %w", err)
	}

	// 返回Base64编码的结果
	return base64.StdEncoding.EncodeToString(finalJsonBytes), nil
}
