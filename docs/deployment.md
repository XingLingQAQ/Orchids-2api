# 部署指南

## Docker 构建

### 使用构建脚本

```bash
./build.sh
```

### 手动构建

```bash
docker build --platform linux/amd64 -f ./Dockerfile -t opus-api:latest .
```

## 运行容器

### 加载镜像

```bash
docker load -i opus-api.tar
```

### 使用 docker-compose

```bash
docker compose up -d
```

### 直接运行

```bash
docker run -d \
  --name orchids-api \
  -p 3002:3002 \
  -v /path/to/data:/app/data \
  -e ADMIN_USER=admin \
  -e ADMIN_PASS=your_password \
  opus-api:latest
```

### 持久化存储

数据库文件存储在容器内的 `/app/data` 目录，包含：
- `orchids.db` - SQLite 数据库（账号配置、API Keys 等）

**必须挂载持久卷**以避免数据丢失：

```yaml
# docker-compose.yml 示例
services:
  opus-api:
    image: opus-api:latest
    ports:
      - "3002:3002"
    volumes:
      - ./data:/app/data  # 持久化数据目录
    environment:
      - ADMIN_USER=admin
      - ADMIN_PASS=your_password
      - API_KEYS_ENABLED=true
```

### 查看日志

```bash
docker compose logs -f opus-api
```

## 本地开发

### 安装依赖

```bash
go mod download
```

### 运行服务

```bash
go run ./cmd/server/main.go
```

## 测试

### 运行测试

```bash
go test ./...
```

### 现有测试

- `internal/tiktoken/tokenizer_test.go`
  - Token 估算测试
  - 文本 Token 测试
  - CJK 字符检测测试
