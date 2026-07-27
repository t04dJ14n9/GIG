# Gig SSA 解释器性能优化记录（2026-06）

本文记录本轮优化过程、依据、结果和后续瓶颈。目标是把新版 gofun 风格 SSA interpreter 的性能拉近 Yaegi，同时保持正确性测试通过；重点验证用 DirectCall wrapper 替代外部函数/方法的反射调用。

## 环境

- Go: `go1.26.3 darwin/arm64`
- CPU: Apple M3 Pro
- Benchmark 模块：`benchmarks`
- Yaegi 版本：`github.com/traefik/yaegi v0.16.1`

主要命令：

```bash
go test ./...
cd examples/custom && go test ./...
cd benchmarks
go test -modfile=/tmp/gig-bench.mod -mod=mod -run '^$' \
  -bench '^Benchmark(Yaegi|Gig)_' \
  -benchmem -count=3
```

## 优化前瓶颈

初始 benchmark 显示新版 SSA interpreter 可读性更好，但性能明显慢于 Yaegi：

| 用例 | Gig 优化前 | Yaegi | 主要问题 |
| --- | ---: | ---: | --- |
| `ArithSum` | ~789 us/op | ~22 us/op | Phi 和 BinOp 每轮分配 Cell，数字转换走 reflect |
| `Fib25` | ~196 ms/op | ~58 ms/op | 每次递归新建 frame/map/cell |
| `ExtCallDirectCall` | ~2.48 ms/op | ~0.76 ms/op | 新后端缺少 DirectCall fast path，外部调用走通用 host path |
| `ExtCallReflect` | ~8.99 ms/op | ~0.45 ms/op | host call 每次构造 reflect args/results |
| `ExtCallMethod` | ~9.05 ms/op | ~0.55 ms/op | 方法调用先扫解释期方法，再查 host method |
| `ExtCallMixed` | ~4.98 ms/op | ~0.39 ms/op | 多种外部调用成本叠加 |

profile 进一步确认：

- `runBlockPhis` 和 `runBinOp` 是算术循环的主要分配源。
- `reflectFunc.Call`、`runCall`、`hostMethodTypeKeys` 是外部调用的主要分配源。
- Yaegi 的 frame 使用 slot/indexed 数据结构，避免了每次通过 `map[ssa.Value]*Cell` 进行运行时查找；这是 Gig 后续还需要补的结构性差距。

## 参考实现启发

### main 分支旧 VM

旧 stack VM 的关键经验是 DirectCall：

- 注册真实 Go 函数，同时可附带 `func([]value.Value) ([]value.Value, error)` wrapper。
- VM 在 external call 时先走 wrapper，缺失时才走 `reflect.Value.Call`。
- 方法也可以为常见 receiver 生成 wrapper。

这条经验可以迁移到新 SSA interpreter 的 host bridge，而不需要恢复 bytecode VM。

### Yaegi

Yaegi 的优势不只是“少用反射”，而是 frame 结构更轻：

- frame 内部是连续 slot，例如 `data []reflect.Value`。
- 变量访问在构建阶段变成 accessor，运行时不需要频繁以 SSA value 为 map key。
- 标准库符号通过 exports 注册，调用边界更稳定。

所以 wrapper 能解决宿主边界反射问题，但不能单独解决纯 SSA 循环慢的问题。

## 已完成改动

### 1. Cell 复用和 Phi 栈缓冲

`frame.setCell` 会复用同一个 SSA instruction 对应的 `Cell`，避免循环里每次重新分配。

`runBlockPhis` 先把 Phi 的输入值算到小数组里，再统一写回，保留 SSA Phi “同时赋值”语义。

闭包绑定现在保存“当前绑定值”的快照，而不是共享外层 frame 的 `Cell` 本身。地址型局部变量在运行时已经表现为 pointer `Value`，所以闭包仍然能通过同一块地址观察和修改变量；但循环体里的同一个静态 `ssa.Alloc` 每轮执行都能产生新的地址，避免多个闭包错误地读到最后一轮的值。

### 2. 基础数值转换 fast path

`arith.go` 为基本 int/uint/float/complex 类型补了 fast path：

- 未命名 basic type 直接构造 `value.Value`。
- 自定义 named type 仍走 converter/reflect，避免丢失方法集和类型身份。
- 常见 `types.Typ[...]` 直接判断，减少 `Underlying`/`Unalias` 开销。

### 3. DirectFunction/DirectMethod fast path

host API 增加可选 fast path：

```go
type DirectFunction interface {
    Function
    CallDirect(args []value.Value) ([]value.Value, bool, error)
}

type DirectMethod interface {
    Method
    CallDirect(recv value.Value, args []value.Value) (value.Value, bool, error)
}
```

解释器在 `runCall` 中先尝试 direct host path，成功时跳过：

- `reflect.Value.Call`

Direct wrapper 返回 `[]value.Value`，解释器再用现有 `packResults` 统一处理
0/1/N 返回值。

方法 DirectCall 的签名是：

```go
func(recv value.Value, args []value.Value) value.Value
```

这比“receiver 放在 args[0]”更清晰，也避免每次方法调用为了拼接 receiver 而额外分配切片。

### 4. Host function/method lookup cache

`program` 现在缓存：

- `hostFuncs`: `*ssa.Function -> host.Function`
- `hostMethods`: `(reflect.Type, method) -> host.Method`

这样外部调用热点不会反复查 registry、拼接 `"pkg.Type"` key 或创建 bridge 对象。

### 5. 保守 frame pool

Frame pool 只在很窄的场景启用：

- 直接递归函数，例如 benchmark 里的 `fib`。
- 简单闭包体，也就是有 `FreeVars`，但函数内部不再创建闭包。
- 函数没有 `fn.Locals`，也不包含 `Alloc`、`MakeClosure`、`defer/panic`、goroutine/select/send 等复杂 frame 状态。

普通顶层函数仍然走：

```text
callSSA -> newFrame -> runFrame -> return
```

这样避免把所有 loop benchmark 都拖进 `sync.Pool` 成本里，同时让递归和闭包调用少分配 frame storage。归还 pool 前会清空：

- `slots`
- `cells`
- `addrRefs`
- `iters`
- `defers`
- panic/recover 状态

这里没有恢复旧 stack VM 的“全局 frame 复用”模型；pool 只是 SSA interpreter 内部的局部优化。

### 6. 扩大 slot cache

`frame` 仍然保留 `map[ssa.Value]*Cell` 作为完整语义模型，但对绝大多数会产生结果的 SSA value 额外建立 `slotIndex`：

- `readValue` 优先从 `[]Cell` 读取，miss 时才回到 map。
- `setCell` 优先写 slot，冷门指令仍写 map。
- 大多数函数每次 `newFrame` 都创建新的 slot storage；只有通过 `framePoolEligible` 的递归/闭包函数会复用 frame。
- `ssa.Alloc` 明确不进 slot：loop closure capture 需要捕获每次执行 `Alloc` 得到的新地址值，而不是共享同一个 SSA instruction cell。

这还不是完整的 Yaegi 式 slot accessor：运行时仍然通过 `map[ssa.Value]int` 找 slot index。当前版本的 `ArithmeticSum` 稳定约 `808 B/op, 8 allocs/op`，已经显著少于早期每轮分配 Cell 的实现。

### 7. IndexAddr side-channel

BubbleSort/Sieve 的 profile 显示剩余大部分分配来自：

```text
runIndexAddr -> reflectValue -> value.makeReflect
```

也就是每次执行 `s[i]` 都把元素地址包装成一个新的 reflect pointer `value.Value`。

本轮增加了 frame 内部的 `addrRefs` side-channel：如果某个 `ssa.IndexAddr` 的所有 referrer 都是 `Store` 或 `UnOp(MUL)`，解释器不再物化 pointer Value，而是把元素位置记录在 frame 内部，由 `runStore` / `runUnOp` 直接消费。

这保留了安全边界：如果地址会逃逸到其它用法，仍走原来的 reflect pointer fallback。

### 8. `[]int` native fast path

在 side-channel 基础上，`[]int` 进一步走 native 表示：

- `MakeSlice` 遇到静态类型 `[]int` 时创建 `value.MakeIntSlice(make([]int, len, cap))`。
- `Index` / `IndexAddr` / `Store` / `UnOp(MUL)` 对 native `[]int` 直接读写。
- `KindSlice` 补了 nil-slice 语义，保证 `var s []int; s == nil` 这类测试仍符合 Go。
- 非 `[]int`、命名 slice、地址逃逸场景继续走 reflect fallback。

这把 `BubbleSort` 从约 `4.25 MB/op, 148k allocs/op` 降到约 `1.4 KB/op, 11 allocs/op`，`Sieve` 从约 `400 KB/op, 13k allocs/op` 降到约 `8.5 KB/op, 7 allocs/op`。

### 9. 保守 fast execution plan

仅靠 slot cache 仍然不够，因为热循环每条指令还要：

```text
ssa.Value -> readValue -> slotIndex map lookup -> Cell -> Value.Int()
```

本轮新增 `internal/interp/fast_plan.go`，在 `frameLayout` 阶段把保守的 plain
`int`/`bool` SSA 指令预编译成小 plan：

- `Phi(int)`：边输入预编译成 slot/const 引用，block entry 直接按
  predecessor edge 读写 `Cell` 内部的 typed cache。
- `BinOp(int)`：`+ - * / %` 直接读取 slot/const，计算后写回 `Cell.fastInt`。
- `BinOp(int -> bool)`：`== != < <= > >=` 直接写回 `Cell.fastBool`。
- `If(bool)`：直接读取条件 slot 并切换 basic block。
- full fast block：如果一个 basic block 的非 Phi 指令全部能走 fast plan，
  `runFrame` 会直接执行 `runFastBlock`，跳过逐条指令的 fallback 判断链。

范围刻意保守：只覆盖未命名 plain `int`/`bool`。命名 int、接口、reflect、
指针、复合类型继续走通用解释路径，避免丢失 Go 类型身份。

这里没有引入 `intSlots` / `boolSlots` 第二套寄存器数组，而是在 `Cell` 内部
放 `fastInt` / `fastBool` / `fastDirty`。fast path 写 typed cache；当结果被
通用路径读取、返回或跨 host 边界使用时，`frame.cell` 会把 typed cache
materialize 回 `value.Value`。这样性能路径集中在 `fast_plan.go`，语义路径
仍然通过 `value.Value` 理解。

### 10. Typed `[]int` IndexAddr plan

`IndexAddr` side-channel 解决了元素地址物化，但仍通过 `readValue` 读 slice、
index 和 store value。本轮把相邻且唯一消费者的：

```text
IndexAddr -> UnOp(*)
IndexAddr -> Store
```

进一步编译成 typed `[]int` load/store plan：

- slice 来源是预计算 slot。
- index/value 是预计算 slot 或 const。
- load/store 时直接访问 native `[]int`。
- 若运行时值不是 native `IntSlice`，回退到原通用路径。

另外，SSA 会把 `make([]int, 100)` 降成：

```text
new [100]int (makeslice)
slice t0[:100]
```

因此 `runSlice` 也补了 `reflectIntSlice` fast path：当切片结果是
`[]int` 时直接存为 `value.MakeIntSlice`。这个改动是 `BubbleSort` 从
约 `5 ms/op` 降到约 `0.96 ms/op` 的关键。

### 11. Direct closure call

原先 `MakeClosure` 产生的是 `reflect.MakeFunc` 包装值。即使调用方也在解释器内部，
间接调用闭包时仍要：

```text
Value -> reflect.Func -> reflect.Value.Call -> []reflect.Value -> callSSA
```

本轮改为让 `MakeClosure` 返回 `value.KindFunc`，payload 是一个
`interpretedFunc`：

- 解释器内部调用时，`runCall` 识别 `interpretedFunc`，直接组装 `[]value.Value`
  并调用 `callSSA`。
- 传给宿主代码时，`interpretedFunc` 仍提供 `ReflectValue()`，converter 可以拿到
  `reflect.MakeFunc` fallback。

配合保守 frame pool 后，`ClosureCalls` 在 `benchmarks` 子模块中约
`490-500 us/op, 113 KB/op, 3,997 allocs/op`，已经接近 Yaegi 的运行时间，
但分配明显更低。

### 12. stdlib wrapper generation

生成文件 `stdlib/packages/*.go` 由 `gentool` 自动生成 package-level function DirectCall wrapper。
`stdlib/packages/zz_direct_wrappers.go` 只保留少量 benchmark 热点方法补丁：

- `(*strings.Reader).Len`
- `(*strings.Replacer).Replace`

当前内置 `stdlib/packages` 的 package-level functions 已全部带 DirectCall；
`examples/custom/mydep/packages` 中的第三方示例包也可通过 gentool 生成同类 wrapper。

## 可读执行模型重构基线（2026-07-11）

以下数据用于约束可读 interpreter 执行模型重构。测量时 HEAD 为
`672ca9a8d4533df5befa94d5201adef28e7dd67c`，工作区保留当时已有的未提交改动。
环境为 Go 1.26.3、darwin/arm64、Apple M3 Pro（12 个逻辑 CPU、36 GiB 内存）。

两个 benchmark 集合都使用 `-benchmem -count=5` 并成功通过。表中 `中位数`、
`B/op` 和 `allocs/op` 均取五次样本的中位数；`3x 硬上限` 是后续重构验收使用的
`3 * 中位数(ns/op)`。

```bash
go test ./tests -run '^$' \
  -bench '^BenchmarkGig_(ArithmeticSum|FibRecursive|FibIterative|SliceSum|NestedLoops|BubbleSort|Sieve|ClosureCalls)$' \
  -benchmem -count=5
(cd benchmarks && go test -run '^$' \
  -bench '^BenchmarkGig_(ArithSum|Fib25|BubbleSort|Sieve|ClosureCalls|ExtCallDirectCall|ExtCallReflect|ExtCallMethod|ExtCallMixed)$' \
  -benchmem -count=5)
```

| Benchmark | 中位数 (ns/op) | 3x 硬上限 (ns/op) | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| `tests/ArithmeticSum` | 38,651 | 115,953 | 968 | 8 |
| `tests/FibIterative` | 2,858 | 8,574 | 1,032 | 8 |
| `tests/FibRecursive` | 673,845 | 2,021,535 | 1,736,877 | 7,900 |
| `tests/SliceSum` | 92,374 | 277,122 | 10,317 | 16 |
| `tests/NestedLoops` | 49,370 | 148,110 | 1,704 | 8 |
| `tests/BubbleSort` | 169,298 | 507,894 | 4,446 | 15 |
| `tests/Sieve` | 169,802 | 509,406 | 11,065 | 9 |
| `tests/ClosureCalls` | 542,285 | 1,626,855 | 729,878 | 4,997 |
| `benchmarks/ArithSum` | 37,960 | 113,880 | 968 | 8 |
| `benchmarks/Fib25` | 77,557,747 | 232,673,241 | 213,651,922 | 971,151 |
| `benchmarks/BubbleSort` | 654,700 | 1,964,100 | 4,934 | 15 |
| `benchmarks/Sieve` | 170,000 | 510,000 | 11,065 | 9 |
| `benchmarks/ClosureCalls` | 539,168 | 1,617,504 | 729,904 | 4,997 |
| `benchmarks/ExtCallDirectCall` | 720,676 | 2,162,028 | 460,541 | 21,394 |
| `benchmarks/ExtCallReflect` | 433,740 | 1,301,220 | 240,004 | 9,676 |
| `benchmarks/ExtCallMethod` | 463,872 | 1,391,616 | 309,666 | 12,652 |
| `benchmarks/ExtCallMixed` | 355,634 | 1,066,902 | 248,643 | 9,708 |

## 历史：可读性重构前的阶段性性能提升记录

> 历史说明：本节记录的是可读执行模型重构前的实现。下文提到的
> `slot cache`、`IndexAddr side-channel`、typed/fast execution plan 已在后续
> 重构中删除或由统一 cell storage 与 ordered block plan 取代，不代表当前架构。

下面的提升倍数用于记录优化方向和量级。`优化前` 来自本轮 SSA interpreter
优化开始时的基线；`当前` 来自 Apple M3 Pro 上 `benchmarks` 子模块
`-count=5` 的均值。不同轮次 benchmark 会有正常抖动，因此这里关注数量级和
主要瓶颈变化。

### 总体前后对比

| 用例 | 优化前 | 当前 | 提升 | 主要贡献 |
| --- | ---: | ---: | ---: | --- |
| `ArithSum` | ~789 us/op | 40.0 us/op | ~19.7x | Cell 复用、Phi 栈缓冲、基础数值 fast path、typed int/bool fast plan |
| `Fib25` | ~196 ms/op | 57.88 ms/op | ~3.4x | 保守 frame pool、slot cache、递归调用路径瘦身 |
| `BubbleSort` | ~5 ms/op | 644.0 us/op | ~7.8x | `[]int` native fast path、IndexAddr side-channel、typed load/store plan |
| `Sieve` | ~400 KB/op 分配级别 | 161.5 us/op / 11.0 KB/op | 分配显著下降 | `[]int` native fast path、typed IndexAddr plan、`runSlice` fast path |
| `ClosureCalls` | reflect 闭包往返 | 445.6 us/op / 113.6 KB/op | 接近 Yaegi，分配低 | `interpretedFunc` direct closure call、保守 frame pool |
| `ExtCallDirectCall` | ~2.48 ms/op | 673.6 us/op | ~3.7x | DirectFunction fast path、gentool package-level wrapper |
| `ExtCallReflect` | ~8.99 ms/op | 372.2 us/op | ~24.2x | host lookup cache、DirectCall 覆盖原本混合 reflect 的 stdlib 函数 |
| `ExtCallMethod` | ~9.05 ms/op | 407.9 us/op | ~22.2x | DirectMethod、method lookup cache、receiver shape 调整缓存 |
| `ExtCallMixed` | ~4.98 ms/op | 327.8 us/op | ~15.2x | function DirectCall + method DirectCall + host cache 叠加 |

### DirectCall 开关对比

为了单独衡量 DirectCall，本轮额外在 `/tmp` 副本里禁用
`host.registryBridge.LookupFunc` 读取 `obj.DirectCall`，并禁用
`LookupMethodDirectCall`，再跑同一组外部调用 benchmark。这个对比隔离了
“当前其它解释器优化已经存在，只关闭 DirectCall fast path”时的退化幅度。

| 用例 | DirectCall 开启 | DirectCall 禁用 | 加速 | B/op 下降 | allocs/op 下降 |
| --- | ---: | ---: | ---: | ---: | ---: |
| `ExtCallDirectCall` | 697.3 us/op | 1307.1 us/op | 1.87x | -23.9% | -21.9% |
| `ExtCallReflect` | 380.4 us/op | 8577.8 us/op | 22.55x | -98.1% | -92.2% |
| `ExtCallMethod` | 415.1 us/op | 8160.0 us/op | 19.66x | -97.6% | -90.0% |
| `ExtCallMixed` | 335.7 us/op | 4476.5 us/op | 13.34x | -96.2% | -85.8% |

DirectCall 的收益不是来自“第三方函数本身返回 error 与否”，而是来自绕过宿主边界
最贵的 `reflect.Value.Call` / `MethodByName` 调用。真实函数的 0/1/N 个返回值
都放进 `[]value.Value`；wrapper 自身的参数转换、返回值转换、变参拆包失败才走
外层 `error`。

### 单项优化记录

| 优化项 | 记录到的效果 | 说明 |
| --- | --- | --- |
| Cell 复用 + Phi 栈缓冲 | `ArithSum` 从数百 us 级降到几十 us 级 | 消除循环内反复为 SSA instruction 分配 `Cell` 的问题 |
| 基础数值 fast path | 数值运算不再频繁走 converter/reflect | 未命名 basic type 直接构造 `value.Value` |
| 保守 frame pool | `Fib25` 从 ~196 ms/op 降到 ~58 ms/op | 只覆盖直接递归和简单闭包，避免普通函数被 `sync.Pool` 成本拖慢 |
| `[]int` native fast path | `BubbleSort` 从约 `4.25 MB/op, 148k allocs/op` 降到 KB/十几次分配级别 | 对静态 `[]int` 的 MakeSlice/Index/Store/Load 使用 native 表示 |
| IndexAddr side-channel | 避免 `s[i]` 每次物化 reflect pointer | 仅在 `IndexAddr` 唯一消费者是 `Store`/`UnOp(MUL)` 时启用 |
| fast execution plan | 热点 int/bool SSA 指令减少 dispatch 和 map lookup | 预编译 Phi/BinOp/If 的 slot/const 引用 |
| direct closure call | 闭包内部调用跳过 `reflect.MakeFunc` 往返 | `MakeClosure` 产出 `interpretedFunc`，宿主边界仍保留 reflect fallback |
| gentool DirectCall generation | stdlib package-level functions 690/690 direct，示例依赖 741/741 direct | 支持多返回值、variadic 拆包和第三方包路径前缀命名 |

## 当时结果（历史快照）

本轮校验命令：

```bash
cd benchmarks
go test -run '^$' \
  -bench '^Benchmark(Gig|Yaegi)_' \
  -benchmem -count=5
```

结果为 Apple M3 Pro 上 5 次运行的均值；微基准有正常抖动。

| 用例 | Gig 当前 | Yaegi | 结论 |
| --- | ---: | ---: | --- |
| `Fib25` | 57.88 ms/op, 33.07 MB/op | 53.74 ms/op, 98.69 MB/op | Yaegi 快 1.08x，Gig 分配更低 |
| `ArithmeticSum` | 40.0 us/op, 952 B/op | 23.8 us/op, 320 B/op | Yaegi 快 1.68x |
| `BubbleSort` | 644.0 us/op, 4.9 KB/op | 676.9 us/op, 42.5 KB/op | Gig 快 1.05x，分配更低 |
| `Sieve` | 161.5 us/op, 11.0 KB/op | 114.6 us/op, 9.3 KB/op | Yaegi 快 1.41x |
| `ClosureCalls` | 445.6 us/op, 113.6 KB/op | 446.7 us/op, 465.0 KB/op | 基本持平，Gig 分配更低 |
| `ExtCallDirectCall` | 673.6 us/op, 460.5 KB/op | 754.3 us/op, 328.7 KB/op | Gig 快 1.12x |
| `ExtCallReflect` | 372.2 us/op, 239.9 KB/op | 444.7 us/op, 204.2 KB/op | Gig 快 1.19x |
| `ExtCallMethod` | 407.9 us/op, 309.6 KB/op | 558.6 us/op, 239.8 KB/op | Gig 快 1.37x |
| `ExtCallMixed` | 327.8 us/op, 248.6 KB/op | 386.1 us/op, 170.8 KB/op | Gig 快 1.18x |

结论：Gig 目前不是所有用例都追平 Yaegi。外部函数、方法和混合调用已全部快于 Yaegi；闭包与 BubbleSort 已持平或略快。最明显差距仍在纯算术/筛法这类“小指令、高频 dispatch”循环。

## 当时的正确性验证（历史快照）

本轮改动后通过：

```bash
go test ./internal/interp ./host ./tests -count=1
go test ./...
cd examples/custom && go test ./...
```

新增覆盖：

- `host/registry_bridge_test.go`
  - 验证 function DirectCall 不会落到 reflect 函数体。
  - 验证 method DirectCall 和 `CallDirect` fast path。
- `internal/interp/perf_test.go`
  - 约束 `ArithmeticSum` 分配数，防止 Phi/BinOp Cell 分配回退。
  - 约束 `BubbleSort` 分配数，防止 `IndexAddr` / `[]int` fast path 回退。
  - 约束 `ClosureCalls` 分配数，防止 direct closure call 回退到 reflect 调用。
- `internal/interp/fuse_test.go`
  - 验证 `IndexAddr -> Load/Store` 可融合识别。
  - 验证 `frameLayout` 生成 fast int loop plan 和 typed `[]int` plan。
  - 验证 `runSlice` 能把 SSA `new [N]int` + `slice` 结果保存为 native `IntSlice`。
  - 验证 frame pool 只覆盖直接递归函数和简单闭包体，不覆盖普通顶层函数。

本轮额外验证：

```bash
go test ./tests -run 'TestComplex/(ClosureCaptureLoop|ClosureSliceOfFuncs)|TestTrickyClosures/(ClosureWithLoopVar|ClosureCaptureLoopVarTest)|TestStrangeSyntax/(ClosureInLoop|ClosureCapturingLoopVar)' -count=1
go test ./internal/interp -count=1
go test ./...
```

## 当时的下一阶段路线（已被可读执行模型重构取代）

要继续追 Yaegi，重点不再是外部 wrapper，而是 interpreter 内部表示：

1. **扩大 typed op 覆盖**
   - 本轮覆盖 plain `int`、`bool if`、`[]int` load/store。
   - 后续可扩展到 `[]bool`、`[]string`、`map[string]int`、字符串索引/拼接等热点。

2. **precomputed callsite**
   - `runCall` 每次都从 SSA common 重新读 args、判断 call kind。
   - 可以在 program build 阶段给热点 callsite 生成小 descriptor。

3. **更完整的 slot-indexed frame**
   - 本轮 typed plan 已经绕过部分 `map[ssa.Value]int`。
   - 通用 fallback 仍使用 `readValue`，可以继续把常见指令的 operand 预编译成 slot ref。

4. **闭包 frame/cell 优化**
   - 本轮已经去掉解释器内部闭包调用的 reflect 往返。
   - 后续可以继续区分 escaping cell 和普通 temporary cell，减少 closure call 的剩余分配。

5. **DirectCall wrapper 内部继续瘦身**
   - package-level function wrapper 已由 gentool 生成，下一步重点是减少 wrapper 内部
     `value.Value -> Go 参数` 转换的 reflect 成本。
   - method wrapper 目前仍靠少量手写 overlay，可以继续让 gentool 生成常见方法
     DirectMethod wrapper。

## 可读执行模型重构后验收（2026-07-11）

在同一台 Apple M3 Pro（12 个逻辑 CPU、36 GiB 内存）上，使用 Go 1.26.3、
darwin/arm64，对上述基线的两个 benchmark 集合按顺序重新执行
`-benchmem -count=5`。表中“后/前”是重构后 `ns/op` 中位数除以重构前中位数；
小于 `1.0x` 表示自然加速。`B/op`、`allocs/op` 也分别取五次样本中位数，
括号内为重构后减重构前的差值。

| Benchmark | 重构前 ns/op | 重构后 ns/op | 后/前 | 3x 硬上限 | B/op 前 → 后 (Δ) | allocs/op 前 → 后 (Δ) | 结论 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | :---: |
| `tests/ArithmeticSum` | 38,651 | 55,993 | 1.449x | 115,953 | 968 → 1,336 (+368) | 8 → 9 (+1) | PASS |
| `tests/FibIterative` | 2,858 | 3,495 | 1.223x | 8,574 | 1,032 → 1,336 (+304) | 8 → 9 (+1) | PASS |
| `tests/FibRecursive` | 673,845 | 904,589 | 1.342x | 2,021,535 | 1,736,877 → 2,147,966 (+411,089) | 7,900 → 9,874 (+1,974) | PASS |
| `tests/SliceSum` | 92,374 | 119,542 | 1.294x | 277,122 | 10,317 → 10,296 (-21) | 16 → 16 (0) | PASS |
| `tests/NestedLoops` | 49,370 | 67,446 | 1.366x | 148,110 | 1,704 → 1,904 (+200) | 8 → 12 (+4) | PASS |
| `tests/BubbleSort` | 169,298 | 223,679 | 1.321x | 507,894 | 4,446 → 5,555 (+1,109) | 15 → 15 (0) | PASS |
| `tests/Sieve` | 169,802 | 218,022 | 1.284x | 509,406 | 11,065 → 11,490 (+425) | 9 → 13 (+4) | PASS |
| `tests/ClosureCalls` | 542,285 | 632,458 | 1.166x | 1,626,855 | 729,878 → 1,098,026 (+368,148) | 4,997 → 5,995 (+998) | PASS |
| `benchmarks/ArithSum` | 37,960 | 54,638 | 1.439x | 113,880 | 968 → 1,336 (+368) | 8 → 9 (+1) | PASS |
| `benchmarks/Fib25` | 77,557,747 | 113,292,792 | 1.461x | 232,673,241 | 213,651,922 → 264,152,106 (+50,500,184) | 971,151 → 1,213,939 (+242,788) | PASS |
| `benchmarks/BubbleSort` | 654,700 | 883,137 | 1.349x | 1,964,100 | 4,934 → 6,045 (+1,111) | 15 → 15 (0) | PASS |
| `benchmarks/Sieve` | 170,000 | 220,133 | 1.295x | 510,000 | 11,065 → 11,490 (+425) | 9 → 13 (+4) | PASS |
| `benchmarks/ClosureCalls` | 539,168 | 641,785 | 1.190x | 1,617,504 | 729,904 → 1,098,061 (+368,157) | 4,997 → 5,995 (+998) | PASS |
| `benchmarks/ExtCallDirectCall` | 720,676 | 726,962 | 1.009x | 2,162,028 | 460,541 → 460,869 (+328) | 21,394 → 21,398 (+4) | PASS |
| `benchmarks/ExtCallReflect` | 433,740 | 397,804 | 0.917x | 1,301,220 | 240,004 → 239,755 (-249) | 9,676 → 9,678 (+2) | PASS |
| `benchmarks/ExtCallMethod` | 463,872 | 456,242 | 0.984x | 1,391,616 | 309,666 → 309,746 (+80) | 12,652 → 12,653 (+1) | PASS |
| `benchmarks/ExtCallMixed` | 355,634 | 364,608 | 1.025x | 1,066,902 | 248,643 → 248,843 (+200) | 9,708 → 9,712 (+4) | PASS |

17 个代表性 workload 全部低于精确的 `3.0x` 硬上限。最大耗时变化来自
`benchmarks/Fib25`：`113,292,792 / 77,557,747 = 1.460754x`，仍比
`232,673,241 ns/op` 上限低 `119,380,449 ns/op`。纯算术和递归 workload
承担了主要可读性成本：用一个 canonical `Cell.Value` 取代 slot 与 typed dirty
cache 后，`ArithSum`、`ArithmeticSum` 和 `Fib25` 的耗时约为基线的
`1.44x`–`1.46x`，其中 `Fib25` 的分配中位数增加 `50,500,184 B/op` 和
`242,788 allocs/op`；递归与闭包也体现了统一值表示带来的 materialization 成本。

其余简化的代价保持在硬上限内：一个有序 block plan 与通用 Phi group 取代并行
fast array 后，循环控制密集的 `NestedLoops`、`BubbleSort` 和 `Sieve` 为约
`1.28x`–`1.37x`；一个 planned indexed pair 取代 `addrRef` side state 后，
BubbleSort/Sieve 仍保持 KB 级分配。外部边界改为一次 `ExternalObject` lookup 加
custom-registry fallback，并统一为一条 host-function 和一条 host-method dispatch
路径后，四个外部调用 workload 为 `0.917x`–`1.025x`，说明这部分可读性合并没有
形成显著回退。

需要注意，现有 `ExtCallReflect`、`ExtCallMethod` 和 `ExtCallMixed` 实际会命中已注册
的 `DirectMethod` wrapper，而不是最终的 raw-reflection fallback。因此这些比率衡量
的是统一 host-dispatch 路径，不是纯 reflect-only method 路径。

## 紧凑 Value Frame 性能验收（2026-07-12）

本轮仍在同一台 Apple M3 Pro（12 个逻辑 CPU、36 GiB 内存）上，使用 Go 1.26.3、
darwin/arm64 顺序执行 benchmark；不同 benchmark 命令之间没有并行测量。设计阶段
新鲜的五样本 `BenchmarkGig_Fib25` 中位数为 `150,189,599 ns/op`、
`264,152,537 B/op`、`1,213,943 allocs/op`。

设计阶段的分配 profile 将 `indexCellStore` 直接归因到 40.03% 的对象数和 23.28%
的字节数，frame 构造累计占 60.39% 的对象数；同时，`runReturn` 的单结果 slice
占 19.73% 的对象数，`runCall` 的单参数 slice 占 19.63%。这些数据说明首要问题是
存储所有权和分配，而不是增加新的指令 dispatcher。

### 首轮 gate、失败 profile 与直接调用边界修复

Task 5 首轮七样本中位数为 `61,152,248 ns/op`、`116,537,803 B/op`、
`728,364 allocs/op`：耗时和字节 gate 已通过，但分配数超过 `500,000` 上限。
按失败 workload 重新 profile 后，`runDirectInterpretedCall` 直接占 67.01% 的分配
对象和 13.52% 的分配字节，`newFrameWithLayout` 直接占 32.94% 的对象和 86.39%
的字节。CPU profile 没有发现重复的通用 SSA 指令热点：`runPlannedOp` 与
`visitInstr` 各自仅为 0.19% flat、2.70% cumulative，因此没有增加 `planKind`。

编译器 escape 诊断确认局部 `oneArg` 和 `oneResult` 都移动到堆。根因是单参数
scratch 经过还服务于 host 调用的通用 `readValuesInto` 边界，而单结果 scratch
经过带 defer/recover 和 slice 返回协议的 `callSSAInto` 边界。聚焦回归测试在修复前
为 302 allocs/run；把直接解释调用的 0/1/N 参数读取留在直接 helper，并在单结果
分支直接写回 frame 后为 202 allocs/run。没有引入池、第二套 dispatcher 或 frame
持有的 scratch。

修复后，单参数 `oneArg` 保持在栈上；单结果 `oneResult` 仍因 `callSSAInto` 的
defer/recover 与返回 slice 协议移动到堆。继续消除后者需要扩大结果协议改动，而当前
真实 gate 已通过，因此本轮保留这一明确边界，不用更宽的协议重写换取额外优化。

### 最终七样本 Fib25 gate

| 指标 | 设计前 fresh 基线 | Task 5 首轮 | 最终中位数 | 相对设计基线 Δ | 相对首轮 Δ | gate | gate 余量 | 结论 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: |
| `ns/op` | 150,189,599 | 61,152,248 | 56,636,440 | -93,553,159 (-62.29%) | -4,515,808 (-7.38%) | 135,170,639 | 78,534,199 | PASS |
| `B/op` | 264,152,537 | 116,537,803 | 108,768,715 | -155,383,822 (-58.82%) | -7,769,088 (-6.67%) | 180,000,000 | 71,231,285 | PASS |
| `allocs/op` | 1,213,943 | 728,364 | 485,579 | -728,364 (-60.00%) | -242,785 (-33.33%) | 500,000 | 14,421 | PASS |

最终七样本结果相对上一轮可读模型的 `Fib25` 记录分别为 `0.730249x`（耗时）、
`0.509093x`（字节）和 `0.500004x`（分配数）；相对本轮设计前 fresh 基线则分别为
`0.377100x`、`0.411765x` 和 `0.400001x`。

### 17 个代表性 workload 的最终五样本中位数

“相对原记录”是本次 `ns/op` 除以上表最初记录的可读模型前中位数；“上限占用”是
本次 `ns/op` 除以精确 `3.0x` 上限。`Fib25` 本表使用代表性集合自己的五样本中位数，
因此与上面的七样本 gate 中位数不同。

| Benchmark | ns/op | B/op | allocs/op | 相对原记录 | 精确上限 | 上限占用 | 上限余量 ns (%) | 结论 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: |
| `tests/ArithmeticSum` | 56,625 | 728 | 7 | 1.465033x | 115,953 | 0.488344x | 59,328 (51.1656%) | PASS |
| `tests/FibIterative` | 3,426 | 728 | 7 | 1.198740x | 8,574 | 0.399580x | 5,148 (60.0420%) | PASS |
| `tests/FibRecursive` | 463,332 | 884,635 | 3,953 | 0.687594x | 2,021,535 | 0.229198x | 1,558,203 (77.0802%) | PASS |
| `tests/SliceSum` | 124,320 | 9,168 | 12 | 1.345833x | 277,122 | 0.448611x | 152,802 (55.1389%) | PASS |
| `tests/NestedLoops` | 68,174 | 872 | 8 | 1.380879x | 148,110 | 0.460293x | 79,936 (53.9707%) | PASS |
| `tests/BubbleSort` | 230,672 | 2,219 | 11 | 1.362521x | 507,894 | 0.454174x | 277,222 (54.5826%) | PASS |
| `tests/Sieve` | 227,584 | 9,562 | 9 | 1.340290x | 509,406 | 0.446763x | 281,822 (55.3237%) | PASS |
| `tests/ClosureCalls` | 480,648 | 488,804 | 3,990 | 0.886338x | 1,626,855 | 0.295446x | 1,146,207 (70.4554%) | PASS |
| `benchmarks/ArithSum` | 56,806 | 728 | 7 | 1.496470x | 113,880 | 0.498823x | 57,074 (50.1177%) | PASS |
| `benchmarks/Fib25` | 56,909,038 | 108,768,719 | 485,579 | 0.733763x | 232,673,241 | 0.244588x | 175,764,203 (75.5412%) | PASS |
| `benchmarks/BubbleSort` | 914,919 | 2,708 | 11 | 1.397463x | 1,964,100 | 0.465821x | 1,049,181 (53.4179%) | PASS |
| `benchmarks/Sieve` | 236,750 | 9,562 | 9 | 1.392647x | 510,000 | 0.464216x | 273,250 (53.5784%) | PASS |
| `benchmarks/ClosureCalls` | 480,780 | 488,804 | 3,990 | 0.891707x | 1,617,504 | 0.297236x | 1,136,724 (70.2764%) | PASS |
| `benchmarks/ExtCallDirectCall` | 784,435 | 459,901 | 21,394 | 1.088471x | 2,162,028 | 0.362824x | 1,377,593 (63.7176%) | PASS |
| `benchmarks/ExtCallReflect` | 438,303 | 238,179 | 9,674 | 1.010520x | 1,301,220 | 0.336840x | 862,917 (66.3160%) | PASS |
| `benchmarks/ExtCallMethod` | 505,323 | 309,138 | 12,651 | 1.089359x | 1,391,616 | 0.363120x | 886,293 (63.6880%) | PASS |
| `benchmarks/ExtCallMixed` | 394,921 | 247,811 | 9,708 | 1.110470x | 1,066,902 | 0.370157x | 671,981 (62.9843%) | PASS |

17/17 workload 全部通过。最紧的 `benchmarks/ArithSum` 使用 49.8823% 的精确上限，
仍留有 `57,074 ns/op`（50.1177%）余量。

外部调用数据仍有同一 caveat：`ExtCallReflect`、`ExtCallMethod` 和 `ExtCallMixed`
实际会命中已注册的 `DirectMethod` wrapper，而不是最终 raw-reflection fallback；这些
结果验证的是统一 host-dispatch 与 DirectMethod-backed 路径，不代表纯 reflect-only
method 性能。

### 非返回单结果 sink 复验（Task 7，2026-07-12）

在保留上述 Task 5 原始证据的基础上，本轮把直接解释调用的单结果协议改为调用方
提供、且不会作为 slice 返回的 `*value.Value` sink。聚焦分配测试从
`202 allocs/run` 降到 `102 allocs/run`，减少 `100`（`-49.5050%`），通过
`<= 120` 的新上限。Go 1.26.3 escape 诊断显示所有 `singleResult` 参数均
`does not escape`，且 `oneArg`、`oneResult` 都没有 `escapes to heap` 或
`moved to heap` 诊断；没有增加池、第二套 dispatcher 或新 `planKind`。

同一环境顺序重跑七样本 `BenchmarkGig_Fib25` 后，三个中位数均继续通过 compact
gate，并相对上面的 Task 5 最终中位数进一步下降：

| 指标 | Task 5 最终中位数 | Task 7 最终中位数 | Δ | gate | gate 余量 | 结论 |
| --- | ---: | ---: | ---: | ---: | ---: | :---: |
| `ns/op` | 56,636,440 | 51,841,788 | -4,794,652 (-8.4657%) | 135,170,639 | 83,328,851 | PASS |
| `B/op` | 108,768,715 | 100,999,547 | -7,769,168 (-7.1428%) | 180,000,000 | 79,000,453 | PASS |
| `allocs/op` | 485,579 | 242,793 | -242,786 (-49.9993%) | 500,000 | 257,207 | PASS |

两个五样本集合也按原命令完整重跑。`Fib25` 在下表使用五样本集合自己的中位数，
与上面的七样本 gate 中位数不同。

| Benchmark | ns/op | B/op | allocs/op | 精确上限 | 上限占用 | 上限余量 ns (%) | 结论 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | :---: |
| `tests/ArithmeticSum` | 56,029 | 728 | 7 | 115,953 | 0.483204x | 59,924 (51.6796%) | PASS |
| `tests/FibIterative` | 3,432 | 728 | 7 | 8,574 | 0.400280x | 5,142 (59.9720%) | PASS |
| `tests/FibRecursive` | 424,365 | 821,499 | 1,980 | 2,021,535 | 0.209922x | 1,597,170 (79.0078%) | PASS |
| `tests/SliceSum` | 122,409 | 9,168 | 12 | 277,122 | 0.441715x | 154,713 (55.8285%) | PASS |
| `tests/NestedLoops` | 68,052 | 872 | 8 | 148,110 | 0.459469x | 80,058 (54.0531%) | PASS |
| `tests/BubbleSort` | 228,719 | 2,219 | 11 | 507,894 | 0.450328x | 279,175 (54.9672%) | PASS |
| `tests/Sieve` | 224,858 | 9,562 | 9 | 509,406 | 0.441412x | 284,548 (55.8588%) | PASS |
| `tests/ClosureCalls` | 479,471 | 488,804 | 3,990 | 1,626,855 | 0.294723x | 1,147,384 (70.5277%) | PASS |
| `benchmarks/ArithSum` | 56,371 | 728 | 7 | 113,880 | 0.495004x | 57,509 (50.4996%) | PASS |
| `benchmarks/Fib25` | 51,555,034 | 100,999,594 | 242,794 | 232,673,241 | 0.221577x | 181,118,207 (77.8423%) | PASS |
| `benchmarks/BubbleSort` | 898,321 | 2,708 | 11 | 1,964,100 | 0.457370x | 1,065,779 (54.2630%) | PASS |
| `benchmarks/Sieve` | 225,405 | 9,562 | 9 | 510,000 | 0.441971x | 284,595 (55.8029%) | PASS |
| `benchmarks/ClosureCalls` | 480,254 | 488,805 | 3,990 | 1,617,504 | 0.296911x | 1,137,250 (70.3089%) | PASS |
| `benchmarks/ExtCallDirectCall` | 794,425 | 459,901 | 21,394 | 2,162,028 | 0.367444x | 1,367,603 (63.2556%) | PASS |
| `benchmarks/ExtCallReflect` | 440,444 | 238,179 | 9,674 | 1,301,220 | 0.338485x | 860,776 (66.1515%) | PASS |
| `benchmarks/ExtCallMethod` | 502,264 | 309,138 | 12,651 | 1,391,616 | 0.360921x | 889,352 (63.9079%) | PASS |
| `benchmarks/ExtCallMixed` | 396,430 | 247,811 | 9,708 | 1,066,902 | 0.371571x | 670,472 (62.8429%) | PASS |

17/17 workload 再次通过；最紧的 `benchmarks/ArithSum` 使用精确上限的
`0.495004x`，仍有 `57,509 ns/op`（50.4996%）余量。外部调用三项仍保留上述
DirectMethod-backed caveat。
