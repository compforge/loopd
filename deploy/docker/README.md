# Container images

一个多阶段 Dockerfile 构建 loopd 的三个可独立部署组件：

```bash
docker build -f deploy/docker/Dockerfile --target loop-server -t ghcr.io/compforge/loop-server:latest .
docker build -f deploy/docker/Dockerfile --target router -t ghcr.io/compforge/loop-router:latest .
docker build -f deploy/docker/Dockerfile --target web -t ghcr.io/compforge/loop-web:latest .
```

构建上下文必须是仓库根目录。Web 镜像只包含静态文件；`/v1` 的反向代理由 Helm 挂载的 Nginx
配置提供，因此 Web、loop-server 与 Router 仍保持独立进程边界。


## LongHorizon

可选镜像 target 为 `longhorizon`，二进制入口为 `/usr/local/bin/longhorizon`，包含 bash、git、
Python 和 ripgrep。与 Router 使用同一构建入口：

```sh
docker build -f deploy/docker/Dockerfile --target longhorizon -t loop-longhorizon:local .
```

参考 Operator 的工具在 `/workspace/<run_uid>` 工作；这不是每次调用独立的 OS sandbox。
