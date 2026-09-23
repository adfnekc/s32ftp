# s32ftp 设计与实现计划（FTP → S3 协议网关）

> 状态：**已实现并发布**。单元测试 + 端到端测试全部通过（`go test -race ./...`）。
> 使用说明见 [README.md](README.md) / [README.zh-CN.md](README.zh-CN.md)，
> 配置见 [configs/config.example.yaml](configs/config.example.yaml)。

把外部 FTP 客户端请求翻译成 S3 协议转发给 S3 兼容服务（AWS S3 / MinIO / Ceph RGW /
阿里云 OSS 等），再把结果按 FTP 协议返回给客户端。对客户端来说它就是一个普通
FTP(S) 服务器；对后端来说它是一个 S3 客户端。

- 语言：Go 1.26
- FTP 服务端：[ftpserverlib](https://github.com/fclairamb/ftpserverlib)
  （支持 AUTH TLS / PROT / EPSV / REST / MLSD）
- S3 客户端：AWS SDK for Go v2（`service/s3` + `feature/s3/manager`，SigV4）
- 配置：YAML（`gopkg.in/yaml.v3`）+ `S32FTP_*` 环境变量覆盖
- 日志：标准库 `log/slog`（text / json）

## 1. 需求拆解

| 需求 | 落点 |
| --- | --- |
| 接收 FTP 请求 | ftpserverlib 监听控制端口，实现 `MainDriver` + `ClientDriver` |
| 转发 S3 | 自研 `s3fs` 后端，实现 afero.Fs 语义映射到 S3 API |
| 结果回传 FTP | 上传/下载走流式（io.Pipe + multipart / ranged GetObject） |
| 自定义 S3 AK/SK/Bucket/endpoint | `s3.*` 配置 |
| FTP 自定义端口/IP/SSL | `ftp.listen_ip`、`ftp.port`、`ftp.tls.mode` |

## 2. 目录结构

```
cmd/s32ftp/          入口：加载配置、装配、启动、优雅退出
internal/config/     YAML + 环境变量配置、默认值、校验、bcrypt
internal/logging/    slog 初始化
internal/s3fs/       路径映射、os.FileInfo、afero.Fs、流式上传/下载句柄
internal/ftpdriver/  ftpserverlib MainDriver / ClientDriver / TLS
internal/e2e/        端到端测试（FTP 客户端 → 服务 → 假 S3）
configs/             示例配置
```

## 3. 关键设计

### 3.1 FTP 路径 ↔ S3 key

S3 是扁平命名空间，没有目录。映射规则：

```
key = root_prefix + strings.TrimPrefix(path.Clean(ftpPath), "/")
```

- `root_prefix` 归一化为 `""` 或 `"a/b/"`
- 目录 = 对象键的公共前缀；`MKD` 写 `<prefix>/` 的零字节占位对象（`dir_marker`）
- `/` → 根（bucket + root_prefix 下的全部内容）

### 3.2 操作映射

| FTP | S3 |
| --- | --- |
| LIST / NLST / MLSD | `ListObjectsV2` + `Delimiter=/`（CommonPrefixes → 目录，Contents → 文件），分页 |
| STAT / SIZE / MDTM / MLST | `HeadObject`；目录再探测 `ListObjectsV2(MaxKeys=1, Prefix=key+"/")` |
| RETR | `GetObject`（流式），REST 偏移 → `Range: bytes=N-` |
| STOR | `manager.Uploader` 流式 multipart（`io.Pipe`） |
| APPE / REST 续传 | `MultiReader(原内容[0..offset), 本次上传流)` 作为 multipart 源 |
| DELE | `DeleteObject` |
| MKD | `PutObject(key+"/", 0 byte)` |
| RMD | 前缀为空才删除，非空报错 |
| RNFR/RNTO | `CopyObject` + `DeleteObject`；目录按前缀批量 copy/delete |
| SITE CHMOD / MFMT | 空实现（S3 无权限位/时间戳语义），MFMT 在 Settings 中禁用 |

### 3.3 传输实现

- **下载句柄**：`GetObject` body 直接实现 `Read`；`Seek`/REST 按新偏移重新发起
  ranged GET；`ReadAt` 用 range 请求实现。
- **上传句柄**：`io.Pipe` 把 FTP 数据连接的写入喂给 S3 uploader（内部并发 multipart、
  内存有界）。`Close` 等待上传结果；`TransferError`（ABOR/断连）用 `CloseWithError`
  中止 multipart，避免残留分片。
- **Content-Type**：按扩展名 `mime.TypeByExtension` 推断。

### 3.4 鉴权与安全

- FTP 用户来自配置（支持 bcrypt `password_hash`），每个用户可覆盖 bucket /
  root_prefix / 只读。
- 只读用户对写类操作（STOR/APPE/DELE/RNFR/MKD/RMD）返回 550。
- TLS 模式：`off` / `explicit`（AUTH TLS 可选）/ `required`（强制 AUTH TLS）/
  `implicit`（990 隐式 TLS）。
- FTP 数据通道保留 IP 校验（默认 `IPMatchRequired`，可放开）。
- 连接数上限在 `ClientConnected` 阶段拒绝。

### 3.5 错误语义

S3 错误转成 `os.ErrNotExist` / 权限错误等：对象不存在 → `550`，权限不足 → `550`，
S3 超时/5xx → `450`（可重试）。

## 4. 实施阶段（均已完成）

1. 脚手架：go.mod、依赖、Makefile、目录、`.gitignore`
2. config：结构体、默认值、YAML 解析、ENV 覆盖、校验、单测
3. logging：slog 初始化
4. s3fs 基础：path 映射 + FileInfo + backend 客户端构建 + 单测
5. s3fs 元数据：Stat/ReadDir/Mkdir/Remove/RemoveAll/Rename + 单测
6. s3fs 传输：download/upload 句柄（含 REST/APPE/multipart）
7. ftpdriver：MainDriver + ClientDriver + TLS + 只读/上限
8. main：装配、信号、优雅关闭
9. 交付物：example 配置、Dockerfile、docker-compose（含 MinIO）、中英 README
10. 测试：`go vet` + 单测 + 端到端（真实 FTP 客户端 → 本服务 → gofakes3 内存 S3）

## 5. 已知限制

- 一个实例挂载一个 bucket；多 bucket 用多实例或账号级 `bucket` 覆盖
- S3 凭证为服务级，不是每个 FTP 用户一套（架构已预留用户级 backend）
- 不实现 S3 的 ACL / 版本管理 / 对象锁（FTP 无对应语义）
- `SITE CHMOD`、`MFMT` 接受但为空操作
- `RMD` 要求目录为空
- 集成测试用进程内 gofakes3，不依赖 Docker；真实 MinIO 的差异建议另行验证
