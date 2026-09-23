# s32ftp

[![CI](https://github.com/adfnekc/s32ftp/actions/workflows/ci.yml/badge.svg)](https://github.com/adfnekc/s32ftp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/adfnekc/s32ftp)](https://github.com/adfnekc/s32ftp/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/adfnekc/s32ftp)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/adfnekc/s32ftp)](https://goreportcard.com/report/github.com/adfnekc/s32ftp)

[English](README.md) | **简体中文**

把外部 FTP 请求翻译成 S3 协议转发给对象存储的网关服务。FTP 客户端看到的是一台普通
FTP(S) 服务器，后端只有 S3 API。适合给只会 FTP 的老系统/老设备提供一个无状态、
可水平扩展的对象存储入口。

```
FTP client ──FTP/FTPS──▶ s32ftp ──S3 (SigV4)──▶ AWS S3 / MinIO / Ceph RGW / OSS ...
```

## 特性

- **FTP(S) 前端**（基于 [ftpserverlib](https://github.com/fclairamb/ftpserverlib)）
  - 自定义监听 IP、端口、Banner
  - TLS 模式：`off`、`explicit`（AUTH TLS）、`required`（拒绝明文）、`implicit`（990 隐式）
  - 被动端口范围、NAT/Docker 场景的 `public_host`、可关闭主动模式
  - 空闲超时、连接数上限、账号鉴权（明文或 bcrypt）、只读账号
- **S3 后端**（基于 AWS SDK for Go v2）
  - 自定义 `endpoint_url`、`access_key_id`、`secret_access_key`、`session_token`、`bucket`、`region`
  - path-style 寻址（MinIO/Ceph 必需）、自签证书处理、自定义 CA
  - 流式 multipart 上传，内存占用有界，分片并发可调
- **忠实还原 FTP 语义**
  - 目录用前缀 + 零字节 `<dir>/` 占位对象模拟
  - `REST` 断点续传（上传/下载）、`APPE` 追加
  - `RNFR`/`RNTO` 通过 `CopyObject` + `DeleteObject` 实现递归重命名
- **对运维友好**：结构化日志（`log/slog`，text 或 json）、优雅关闭、多账号 bucket/前缀隔离

## 安装

### go install（需要 Go 1.26+）

```bash
go install github.com/adfnekc/s32ftp/cmd/s32ftp@latest
```

二进制名为 `s32ftp`，安装在 `$(go env GOPATH)/bin` 下。

> 因为某个间接依赖要求 Go 1.26，所以本项目的 go directive 为 1.26。默认
> `GOTOOLCHAIN=auto` 时，低版本工具链会自动下载对应版本。

### 预编译二进制

从 [Releases](https://github.com/adfnekc/s32ftp/releases) 下载对应平台的压缩包，
把 `s32ftp` 放进 `PATH` 即可。

### Docker

```bash
docker build -t s32ftp .
docker run --rm -p 2121:2121 -p 50000-50100:50000-50100 \
  -v "$PWD/config.yaml:/etc/s32ftp/config.yaml:ro" s32ftp
```

## 快速开始

### Docker Compose（自带 MinIO）

```bash
docker compose up --build -d
# FTP 入口 127.0.0.1:2121，账号 admin / admin123
curl -T local.txt ftp://admin:admin123@127.0.0.1:2121/remote.txt
curl ftp://admin:admin123@127.0.0.1:2121/remote.txt
```

### 源码运行

```bash
make build
cp configs/config.example.yaml config.yaml   # 按需修改
./bin/s32ftp -config config.yaml
```

### 命令行参数

```
-config string         YAML 配置文件路径
-version               打印版本并退出
-hash-password string  输出给定密码的 bcrypt hash 并退出
```

## 配置

每个配置项都在 [`configs/config.example.yaml`](configs/config.example.yaml) 中有注释。核心项：

| 配置项 | 说明 | 默认值 |
| --- | --- | --- |
| `ftp.listen_ip` / `ftp.port` | 控制通道监听地址 | `0.0.0.0` / `2121` |
| `ftp.banner` | 连接后发送的欢迎语 | `s32ftp FTP-to-S3 gateway ready` |
| `ftp.tls.mode` | `off` / `explicit` / `required` / `implicit` | `off` |
| `ftp.public_host` | PASV 通告 IP（NAT/Docker 场景） | 空 |
| `ftp.passive_port_range` | 如 `50000-50100`；留空由系统分配 | 空 |
| `ftp.disable_active_mode` | 禁用 `PORT`/`EPRT` | `false` |
| `ftp.disable_ip_match` | 允许数据连接来自不同 IP | `false` |
| `ftp.max_clients` | 最大并发连接数，0 不限制 | `0` |
| `ftp.users[].read_only` | 拒绝所有写操作 | `false` |
| `ftp.users[].bucket` / `root_prefix` | 账号级覆盖 | 空 |
| `s3.endpoint_url` | S3 端点，空表示 AWS 官方 | 空 |
| `s3.access_key_id` / `secret_access_key` | 静态凭证；留空走 AWS 默认凭证链 | 空 |
| `s3.bucket` | 目标 bucket（必填） | — |
| `s3.root_prefix` | 对象统一存放前缀 | 空 |
| `s3.force_path_style` | MinIO/Ceph 需要 `true` | `true` |
| `s3.insecure_skip_verify` / `ca_bundle` | 自签证书处理 | `false` / 空 |
| `s3.part_size_mb` | multipart 分片大小（≥5） | `8` |
| `s3.upload_concurrency` | 分片并发数 | `4` |
| `s3.create_bucket_if_missing` | 启动时创建 bucket | `false` |
| `s3.dir_marker` | 写 `<dir>/` 占位对象，让空目录可见 | `true` |
| `logging.level` / `format` / `output` | `debug\|info\|warn\|error`、`text\|json`、`stdout\|stderr\|<路径>` | `info` / `text` / `stdout` |

### 环境变量覆盖

容器部署推荐使用，前缀 `S32FTP_`，层级用 `_` 连接：

```bash
S32FTP_FTP_PORT=2121
S32FTP_FTP_PUBLIC_HOST=203.0.113.10
S32FTP_FTP_PASSIVE_PORT_RANGE=50000-50100
S32FTP_S3_ENDPOINT_URL=https://s3.example.com
S32FTP_S3_ACCESS_KEY_ID=...
S32FTP_S3_SECRET_ACCESS_KEY=...
S32FTP_S3_BUCKET=ftp
S32FTP_S3_FORCE_PATH_STYLE=true
S32FTP_LOG_LEVEL=debug
```

### 密码

```bash
s32ftp -hash-password 'my secret'   # 输出 bcrypt hash
```

把结果填入 `ftp.users[].password_hash`（与 `password` 二选一）。

## 支持的 FTP 命令

| FTP 命令 | 映射到 | 备注 |
| --- | --- | --- |
| `USER` `PASS` `QUIT` `NOOP` `FEAT` `SYST` `OPTS` `CLNT` | — | 会话管理 |
| `AUTH` `PBSZ` `PROT` | — | FTPS |
| `PWD` `CWD` `CDUP` | 前缀导航 | |
| `LIST` `NLST` `MLSD` `MLST` | `ListObjectsV2`（`Delimiter=/`） | 自动分页 |
| `SIZE` `MDTM` `STAT` | `HeadObject` | |
| `RETR` | `GetObject` | 流式 |
| `RETR` + `REST` | `GetObject` + `Range` | 断点续传下载 |
| `STOR` | 流式 multipart `UploadPart` | 内存有界 |
| `STOR` + `REST` | 已有 `[0,offset)` + 新数据 | 断点续传上传 |
| `APPE` | 已有对象 + 新数据 | 追加 |
| `DELE` | `DeleteObject` | |
| `MKD` `XMKD` | `PutObject("<dir>/")` | 目录占位对象 |
| `RMD` `XRMD` | 非空报错；空目录删除占位对象 | |
| `RNFR` `RNTO` | `CopyObject` + `DeleteObject`，前缀递归 | |
| `ALLO` | 空实现 | S3 无需预分配 |
| `SITE CHMOD` | 空实现（返回 200） | S3 无权限位 |
| `MFMT` | 禁用 | S3 不能改对象时间 |
| `HASH` `AVBL` `SYMLINK` `COMB` | 未实现 | 返回 502 |

## 设计要点

### 路径映射

```
key = root_prefix + strings.TrimPrefix(path.Clean(ftpPath), "/")
```

- `root_prefix` 归一化为 `""` 或 `a/b/`
- 目录 = 对象键的公共前缀；`MKD` 写 `<prefix>/` 零字节对象，使空目录对 `LIST` 可见
- `Stat` 先 `HeadObject(key)`，再 `ListObjectsV2(Prefix=key+"/", MaxKeys=1)`，
  因此「只有子对象、没有占位对象」的虚拟目录也能被识别

### 传输实现

- **下载**：直接读取 `GetObject` 的 body；`Seek`/`REST` 用 ranged GET 重新打开；
  `ReadAt` 使用独立的 range 请求
- **上传**：`io.Pipe` 把 FTP 数据连接喂给 `manager.Uploader`（内部并发 multipart）。
  `Close` 等待上传完成；`ABOR`/断连会触发 `TransferError`，中止 multipart，
  不会留下孤儿分片
- `Content-Type` 按扩展名推断

### 错误语义

S3 的 `NoSuchKey`/`NotFound` → `os.ErrNotExist` → FTP `550`；只读账号的写操作 → `550`；
S3 服务端错误 → `450`（可重试）。

### 部署注意

- 在 NAT 或 Docker 后面，必须设置 `ftp.public_host` 并放通
  `ftp.passive_port_range`，否则被动模式会失败
- S3 端点使用私有 CA 时，优先用 `ca_bundle` 而不是 `insecure_skip_verify`
- `read_only: true` 会拒绝 `STOR`、`APPE`、`DELE`、`RNFR`、`MKD`、`RMD`

## 开发

```bash
make build        # 构建 bin/s32ftp
make test         # 单元测试 + 端到端测试
make test-race    # 竞态检测
make vet
make docker-up    # docker compose 启动 s32ftp + MinIO
```

端到端测试（`internal/e2e`）在进程内启动
[gofakes3](https://github.com/johannesboyne/gofakes3) 作为 S3 服务，再用真实 FTP 客户端
（`jlaffaye/ftp`）跑完整链路，不依赖 Docker / 网络。覆盖：上传/下载/列表/删除、
目录与递归重命名、`APPE`、`REST` 续传、12 MiB multipart、空文件、只读账号、
显式 TLS、`root_prefix` 隔离。

## 已知限制

- 一个实例挂载一个 bucket；多 bucket 用多实例或账号级 `bucket` 覆盖
- S3 凭证为服务级，不是每个 FTP 用户一套。如需支持，可在 `ftp.users[]` 增加字段，
  并在 `AuthUser` 中构造独立 backend（架构已预留）
- 不实现 S3 的 ACL / 版本管理 / 对象锁（FTP 无对应语义）
- `SITE CHMOD`、`MFMT` 接受但不生效
- `RMD` 要求目录为空

## 许可证

[MIT](LICENSE)
