package duckgo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
)

// BrowserInstance 浏览器单例结构
type BrowserInstance struct {
	ctx            context.Context
	cancel         context.CancelFunc
	allocatorCtx   context.Context
	initialized    bool
	initError      error
	mutex          sync.RWMutex
	chromeExecPath string
	userDataDir    string
}

var (
	browserInstance *BrowserInstance
	browserOnce     sync.Once
)

// sha256AndBase64 使用 SHA-256 对字符串进行哈希，然后将结果进行 Base64 编码。
func sha256AndBase64(input string) string {
	h := sha256.New()
	h.Write([]byte(input))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// findChromeExecutablePath 根据操作系统尝试查找 Chrome/Chromium 的可执行路径。
func findChromeExecutablePath() string {
	var paths []string

	switch runtime.GOOS {
	case "darwin": // macOS
		paths = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/usr/bin/google-chrome",
		}
	case "windows": // Windows
		paths = []string{
			os.Getenv("PROGRAMFILES") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("PROGRAMFILES(X86)") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("LOCALAPPDATA") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("PROGRAMFILES") + "\\Chromium\\Application\\chrome.exe",
			os.Getenv("PROGRAMFILES(X86)") + "\\Chromium\\Application\\chrome.exe",
			os.Getenv("LOCALAPPDATA") + "\\Chromium\\Application\\chrome.exe",
		}
	case "linux": // Linux
		paths = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
			"/opt/google/chrome/google-chrome",
		}
	default:
		log.Printf("不支持的操作系统: %s, 无法自动查找 Chrome 路径。", runtime.GOOS)
		return ""
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	log.Println("未能自动找到 Chrome/Chromium 可执行文件，chromedp 将尝试在 PATH 中查找。")
	return ""
}

// killZombieChrome 查找并杀死可能的僵尸Chrome进程
func killZombieChrome() error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux", "darwin":
		// 查找Chrome进程
		cmd = exec.Command("pgrep", "-f", "chrome|chromium")
	case "windows":
		// Windows使用tasklist查找Chrome进程
		cmd = exec.Command("tasklist", "/FI", "IMAGENAME eq chrome.exe", "/FO", "CSV")
	default:
		return fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}

	output, err := cmd.Output()
	if err != nil {
		// 如果没有找到进程，这是正常的
		return nil
	}

	if len(output) == 0 {
		return nil
	}

	log.Println("检测到可能的僵尸Chrome进程，正在清理...")

	switch runtime.GOOS {
	case "linux", "darwin":
		// 解析进程ID并杀死
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if pid, err := strconv.Atoi(line); err == nil {
				if killCmd := exec.Command("kill", "-TERM", strconv.Itoa(pid)); killCmd.Run() != nil {
					// 如果TERM失败，尝试KILL
					exec.Command("kill", "-KILL", strconv.Itoa(pid)).Run()
				}
			}
		}
	case "windows":
		// Windows使用taskkill
		exec.Command("taskkill", "/F", "/IM", "chrome.exe").Run()
	}

	// 等待一段时间让进程完全退出
	time.Sleep(1 * time.Second)
	log.Println("僵尸Chrome进程清理完成")
	return nil
}

// createUserDataDir 创建临时用户数据目录
func createUserDataDir() (string, error) {
	tempDir, err := os.MkdirTemp("", "chrome_user_data_*")
	if err != nil {
		return "", fmt.Errorf("创建临时用户数据目录失败: %w", err)
	}
	return tempDir, nil
}

// GetBrowserInstance 获取浏览器单例实例
func GetBrowserInstance() *BrowserInstance {
	browserOnce.Do(func() {
		browserInstance = &BrowserInstance{}
	})
	return browserInstance
}

// Initialize 初始化浏览器实例（懒加载）
func (b *BrowserInstance) Initialize() error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.initialized {
		return b.initError
	}

	//log.Println("正在初始化 Chrome 浏览器实例...")

	// 查找Chrome可执行文件路径
	b.chromeExecPath = findChromeExecutablePath()

	// 清理可能的僵尸进程
	if err := killZombieChrome(); err != nil {
		log.Printf("清理僵尸进程时出错: %v", err)
	}

	// 创建用户数据目录
	var err error
	b.userDataDir, err = createUserDataDir()
	if err != nil {
		b.initError = err
		return b.initError
	}

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
		chromedp.UserDataDir(b.userDataDir),
	}

	// 如果找到了Chrome路径，添加到选项中
	if b.chromeExecPath != "" {
		allocatorOptions = append(allocatorOptions, chromedp.ExecPath(b.chromeExecPath))
	}

	// 创建分配器上下文
	b.allocatorCtx, _ = chromedp.NewExecAllocator(context.Background(), allocatorOptions...)

	// 创建浏览器上下文
	b.ctx, b.cancel = chromedp.NewContext(b.allocatorCtx)

	// // 快速启动验证（减少超时时间）
	// ctx, cancel := context.WithTimeout(b.ctx, 60*time.Second)
	// defer cancel()
	// // 预热浏览器 - 使用最简单的操作
	// if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
	// 	b.initialized = false
	// 	b.initError = fmt.Errorf("启动 Chrome 浏览器失败: %w", err)
	// 	b.cleanup()
	// 	return b.initError
	// }

	b.initialized = true
	// log.Println("Chrome 浏览器实例初始化完成")
	return nil
}

// cleanup 清理资源
func (b *BrowserInstance) cleanup() {
	if b.cancel != nil {
		b.cancel()
	}
	if b.userDataDir != "" {
		os.RemoveAll(b.userDataDir)
	}
}

// Shutdown 关闭浏览器实例
func (b *BrowserInstance) Shutdown() {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if !b.initialized {
		return
	}

	//log.Println("正在关闭 Chrome 浏览器实例...")
	b.cleanup()
	b.initialized = false
	go killZombieChrome()
	//log.Println("Chrome 浏览器实例已关闭")
}

// ExecuteJS 在浏览器中执行JavaScript
func (b *BrowserInstance) ExecuteJS(jsCode string) (map[string]interface{}, error) {
	// 确保浏览器已初始化
	if err := b.Initialize(); err != nil {
		return nil, fmt.Errorf("浏览器初始化失败: %w", err)
	}

	b.mutex.RLock()
	defer b.mutex.RUnlock()

	if !b.initialized {
		return nil, errors.New("浏览器未初始化")
	}

	// 创建任务上下文
	taskCtx, cancelTask := context.WithTimeout(b.ctx, 30*time.Second)
	defer cancelTask()

	var result map[string]interface{}

	// 执行JavaScript
	tasks := chromedp.Tasks{
		chromedp.Navigate("about:blank"),
		chromedp.Evaluate(jsCode, &result),
	}

	if err := chromedp.Run(taskCtx, tasks); err != nil {
		return nil, fmt.Errorf("执行 JavaScript 失败: %w", err)
	}
	b.initialized = false
	return result, nil
}

// ExecuteObfuscatedJs 执行经过Base64编码的混淆JavaScript
func ExecuteObfuscatedJs(base64EncodedJs string) (string, error) {
	// 获取浏览器实例
	browser := GetBrowserInstance()

	// 解码Base64编码的JavaScript
	decodedJsBytes, err := base64.StdEncoding.DecodeString(base64EncodedJs)
	if err != nil {
		return "", fmt.Errorf("解码 Base64 JS 字符串失败: %w", err)
	}
	decodedJs := string(decodedJsBytes)

	// 执行JavaScript
	rawJsResult, err := browser.ExecuteJS(decodedJs)
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

// setupGracefulShutdown 设置优雅关闭
func setupGracefulShutdown() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		log.Println("收到退出信号，正在优雅关闭...")

		// 关闭浏览器实例
		if browserInstance != nil {
			browserInstance.Shutdown()
		}

		os.Exit(0)
	}()

	// 确保程序退出时关闭浏览器
	defer func() {
		if browserInstance != nil {
			browserInstance.Shutdown()
		}
	}()
}
