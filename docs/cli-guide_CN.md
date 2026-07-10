# Gig CLI 指南

`gig` 命令行工具用于生成依赖注册包，并输出解释程序的可读 SSA。它提供
`init`、`gen` 和 `dump` 三个命令。

## 安装

Gig 要求 Go 1.23.1 或更高版本。

```bash
go install github.com/t04dJ14n9/gig/cmd/gig@latest
```

## 依赖包工作流

Gig 解释 Go 代码时不会动态导入任意包。应用需要预先注册解释程序可以使用的
包。

### `gig init`

创建包含 `pkgs.go` 模板的依赖包目录：

```bash
gig init -package mydep
```

包名为必填项，并且必须是有效的 Go 标识符。该命令会创建：

```text
mydep/
└── pkgs.go
```

编辑 `mydep/pkgs.go`，用空白导入声明需要暴露的包：

```go
package mydep

import (
	_ "fmt"
	_ "strings"
	_ "github.com/spf13/cast"
)
```

### `gig gen`

根据 `<dir>/pkgs.go` 中的导入生成注册包装代码：

```bash
gig gen ./mydep
```

生成的文件会写入 `<dir>/packages/`：

```text
mydep/
├── pkgs.go
└── packages/
    ├── fmt.go
    ├── strings.go
    └── github_com_spf13_cast.go
```

在应用中通过空白导入执行注册：

```go
import _ "your/module/mydep/packages"
```

生成的包装代码会向 Gig 注册包中导出的函数、常量、变量和类型。

## 检查解释程序

### `gig dump`

编译 Gig 源码并输出可读的 SSA：

```bash
gig dump program.go
```

使用 `-` 从标准输入读取源码：

```bash
printf 'package main; func Add(a, b int) int { return a + b }' | gig dump -
```

使用 `--raw` 直接传入源码：

```bash
gig dump --raw 'package main; func main() {}'
```

可选的 `--allow-panic` 标志允许在构建调试输出时使用 panic、recover 和
defer：

```bash
gig dump --allow-panic program.go
```

`--raw` 不能与文件参数同时使用。未使用 `--raw` 时，必须且只能提供一个
文件路径或 `-`。

## 故障排查

### `pkgs.go not found`

生成包装代码前，先初始化依赖包：

```bash
gig init -package mydep
gig gen ./mydep
```

### `no imports found`

在 `<dir>/pkgs.go` 中加入至少一个空白导入，然后重新运行 `gig gen`。

### 无法生成某个包

确认当前模块和工具链可以找到该包：

```bash
go get <package-path>
go list <package-path>
```

然后重新运行 `gig gen`。

## 相关文档

- [README_CN.md](../README_CN.md) — 库概览与示例
- [ARCHITECTURE_CN.md](ARCHITECTURE_CN.md) — 内部架构
- [examples/](../examples/) — 示例程序
