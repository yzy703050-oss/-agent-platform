# 框架与示例来源

聊天执行与恢复架构参考 CloudWeGo 的 [Eino](https://github.com/cloudwego/eino) 和 [chatwitheino example](https://github.com/cloudwego/eino-examples/tree/main/quickstart/chatwitheino)。本项目的平台存储、身份、页面归属和 HTTP 层通过独立适配接入框架。

Go 与 npm 依赖遵循各自版本附带的许可证；依赖锁定见 backend/go.mod、backend/go.sum 与 frontend/package-lock.json。分发第三方源代码或二进制时，应保留其所要求的许可证与 notices。本说明不替用户选择平台自有代码的开源许可证。

政策连接使用 [MCP 官方 Go SDK](https://github.com/modelcontextprotocol/go-sdk)（v1.8.0，MIT）；外部 RAG 的代码、模型及索引由原项目维护，本仓库只新增明确标记为虚构的演示政策文本。
