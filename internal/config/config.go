// Package config 负责从环境变量和 .env 文件加载 mindAgent 的运行配置。
package config

import (
	"bufio"
	"os"
	"strings"
)

// Config 是启动 Agent 所需的全部配置，均来自环境变量（可写在 .env 中）。
type Config struct {
	// APIKey LLM 服务的鉴权 Key（必填）
	APIKey string
	// BaseURL OpenAI 兼容 API 地址，默认 https://api.openai.com/v1
	// 火山方舟（豆包）示例：https://ark.cn-beijing.volces.com/api/v3
	BaseURL string
	// Model 模型 ID，默认 gpt-4o-mini
	Model string
	// Temperature 采样温度，默认 0.7
	Temperature float64
	// MaxStep ReAct 循环最大步数（防死循环），默认 20
	MaxStep int
}

// Load 先加载当前目录下的 .env（存在则加载），再读取环境变量生成配置。
func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, err
	}

	cfg := &Config{
		APIKey:      os.Getenv("LLM_API_KEY"),
		BaseURL:     getEnvDefault("LLM_BASE_URL", "https://api.openai.com/v1"),
		Model:       getEnvDefault("LLM_MODEL", "gpt-4o-mini"),
		Temperature: 0.7,
		MaxStep:     20,
	}

	if cfg.APIKey == "" {
		return nil, ErrNoAPIKey
	}
	return cfg, nil
}

// ErrNoAPIKey 表示未配置 LLM_API_KEY。
var ErrNoAPIKey = errNoAPIKey{}

type errNoAPIKey struct{}

func (errNoAPIKey) Error() string {
	return "未配置 LLM_API_KEY：请设置环境变量，或复制 .env.example 为 .env 并填写"
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadDotEnv 极简 .env 加载器：KEY=VALUE 每行一条，# 开头为注释，
// 已存在的环境变量不会被覆盖。不引第三方依赖。
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 没有 .env 是正常情况
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k != "" && os.Getenv(k) == "" {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}