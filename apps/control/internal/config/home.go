package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigVersion is the configuration schema version this build understands.
const ConfigVersion = 1

// DefaultHome resolves the Qianshou home directory: QIANSHOU_HOME when set,
// otherwise ~/.qianshou. The directory is owned by Qianshou, not by any
// managed repository.
func DefaultHome() (string, error) {
	if home := os.Getenv("QIANSHOU_HOME"); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户主目录失败：%w", err)
	}
	return filepath.Join(userHome, ".qianshou"), nil
}

// ConfigPath returns the configuration file path inside a home directory.
func ConfigPath(home string) string { return filepath.Join(home, "config.json") }

// parse strictly decodes configuration bytes. Unknown fields fail closed so
// copied GitHub facts and legacy role bindings surface immediately instead of
// drifting beside the contract, and a trailing second JSON document is
// rejected instead of being silently ignored.
func parse(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("配置 JSON 不合法（含未支持字段即失败，配置不得复制 GitHub 事实）：%w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("配置 JSON 不合法：第一个文档之后还有额外内容（尾随文档即失败）")
	}
	return &cfg, nil
}

// Load reads and validates the configuration from a home directory. A missing
// file is an error, never an empty configuration.
func Load(home string) (*Config, error) {
	data, err := os.ReadFile(ConfigPath(home))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("配置不存在：%s（先用 qianshou config migrate 或手写目标形状）", ConfigPath(home))
		}
		return nil, fmt.Errorf("读取配置失败：%w", err)
	}
	cfg, err := parse(data)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes the configuration with the documented permissions: the home
// directory 0700 and the file 0600.
func Save(home string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败：%w", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("创建 Qianshou home 失败：%w", err)
	}
	if err := os.WriteFile(ConfigPath(home), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("写入配置失败：%w", err)
	}
	return nil
}
