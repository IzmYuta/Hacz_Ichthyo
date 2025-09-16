# アーキテクチャ図

## システム全体アーキテクチャ

```mermaid
graph TB
    subgraph "Client Layer"
        WEB[Next.js Web App<br/>Port: 3000]
        MOBILE[Mobile App<br/>PWA]
    end
    
    subgraph "GCP Cloud Run Services"
        API[API Service<br/>Go<br/>Port: 8080<br/>1Gi RAM, 1 CPU<br/>Max: 10 instances]
        HOST[Host Service<br/>Go<br/>Port: 8080<br/>1Gi RAM, 1 CPU<br/>Max: 1 instance]
        LIVEKIT[LiveKit Service<br/>WebRTC SFU<br/>Port: 7880/7881<br/>2Gi RAM, 2 CPU<br/>Max: 3 instances]
    end
    
    subgraph "Data Layer"
        DB[(Cloud SQL<br/>PostgreSQL 15<br/>pgvector extension<br/>10GB SSD)]
        REDIS[(Redis 7.0<br/>1GB<br/>Cache & Queue)]
    end
    
    subgraph "External Services"
        OPENAI[OpenAI Realtime API<br/>GPT-Realtime Model]
        STORAGE[Cloud Storage<br/>Backups & Media]
    end
    
    subgraph "Monitoring & CI/CD"
        MONITORING[Cloud Monitoring<br/>Alerts & Dashboards]
        GITHUB[GitHub Actions<br/>CI/CD Pipeline]
        REGISTRY[Container Registry<br/>Docker Images]
    end
    
    subgraph "Network"
        VPC[VPC Connector<br/>radio24-connector<br/>Min: 2, Max: 3]
        LB[Cloud Load Balancer<br/>HTTPS Termination]
    end
    
    %% Client connections
    WEB --> LB
    MOBILE --> LB
    LB --> API
    LB --> LIVEKIT
    
    %% Service connections
    API --> DB
    API --> REDIS
    API --> LIVEKIT
    HOST --> LIVEKIT
    HOST --> OPENAI
    LIVEKIT --> REDIS
    
    %% CI/CD connections
    GITHUB --> REGISTRY
    REGISTRY --> API
    REGISTRY --> HOST
    REGISTRY --> LIVEKIT
    
    %% Data connections
    API --> STORAGE
    
    %% Monitoring connections
    MONITORING --> API
    MONITORING --> HOST
    MONITORING --> LIVEKIT
    MONITORING --> DB
    MONITORING --> REDIS
    
    %% Network connections
    API --> VPC
    HOST --> VPC
    LIVEKIT --> VPC
    VPC --> DB
    VPC --> REDIS
```

## データフロー図

```mermaid
sequenceDiagram
    participant U as User
    participant W as Web App
    participant A as API Service
    participant H as Host Service
    participant L as LiveKit
    participant O as OpenAI
    participant D as Database
    participant R as Redis
    
    Note over U,R: 24時間AIラジオシステムのデータフロー
    
    %% ユーザー接続
    U->>W: アクセス
    W->>A: 認証・ルーム参加
    A->>D: ユーザー情報取得
    A->>L: LiveKitトークン発行
    A->>W: トークン返却
    W->>L: WebRTC接続
    
    %% 音声配信
    H->>O: Realtime API接続
    H->>L: 音声配信開始
    L->>W: 音声ストリーム配信
    W->>U: 音声再生
    
    %% 投稿処理
    U->>W: PTT音声/テキスト投稿
    W->>A: WebSocket送信
    A->>R: キューに追加
    A->>H: 投稿通知
    H->>O: 投稿内容送信
    O->>H: AI応答
    H->>L: 応答音声配信
    L->>W: 応答音声配信
    W->>U: 応答音声再生
    
    %% データ保存
    A->>D: 投稿データ保存
    A->>D: ベクトル埋め込み保存
    A->>R: リアルタイムデータ更新
```

## マイクロサービス間通信

```mermaid
graph LR
    subgraph "API Service"
        A1[HTTP API]
        A2[WebSocket]
        A3[Database Client]
        A4[Redis Client]
        A5[LiveKit Client]
    end
    
    subgraph "Host Service"
        H1[OpenAI Client]
        H2[LiveKit Publisher]
        H3[Audio Processor]
        H4[Program Director]
    end
    
    subgraph "LiveKit Service"
        L1[WebRTC SFU]
        L2[Audio Mixer]
        L3[Recording]
        L4[Redis Client]
    end
    
    subgraph "Web Service"
        W1[Next.js App]
        W2[WebSocket Client]
        W3[WebRTC Client]
        W4[PTT Handler]
    end
    
    %% API Service connections
    A1 --> A3
    A1 --> A4
    A2 --> A4
    A2 --> A5
    
    %% Host Service connections
    H1 --> H3
    H2 --> H3
    H4 --> H1
    H4 --> H2
    
    %% LiveKit Service connections
    L1 --> L2
    L1 --> L3
    L2 --> L4
    
    %% Web Service connections
    W1 --> W2
    W1 --> W3
    W4 --> W2
    
    %% Inter-service connections
    A5 --> L1
    H2 --> L1
    W3 --> L1
    W2 --> A2
```

## セキュリティアーキテクチャ

```mermaid
graph TB
    subgraph "Security Layers"
        subgraph "External Security"
            WAF[Web Application Firewall]
            DDoS[DDoS Protection]
            SSL[SSL/TLS Termination]
        end
        
        subgraph "Authentication & Authorization"
            IAM[Cloud IAM]
            JWT[JWT Tokens]
            RBAC[Role-Based Access Control]
        end
        
        subgraph "Network Security"
            VPC[VPC Network]
            FW[Firewall Rules]
            NAT[NAT Gateway]
        end
        
        subgraph "Data Security"
            ENC[Encryption at Rest]
            TRANS[Encryption in Transit]
            SECRET[Secret Manager]
        end
        
        subgraph "Monitoring & Compliance"
            AUDIT[Audit Logs]
            MONITOR[Security Monitoring]
            COMPLIANCE[Compliance Checks]
        end
    end
    
    subgraph "Application Services"
        API[API Service]
        HOST[Host Service]
        LIVEKIT[LiveKit Service]
        WEB[Web Service]
    end
    
    subgraph "Data Stores"
        DB[(Database)]
        REDIS[(Redis)]
        STORAGE[(Storage)]
    end
    
    %% Security flow
    WAF --> SSL
    SSL --> IAM
    IAM --> JWT
    JWT --> RBAC
    RBAC --> VPC
    VPC --> FW
    FW --> ENC
    ENC --> TRANS
    TRANS --> SECRET
    SECRET --> AUDIT
    AUDIT --> MONITOR
    MONITOR --> COMPLIANCE
    
    %% Service connections
    COMPLIANCE --> API
    COMPLIANCE --> HOST
    COMPLIANCE --> LIVEKIT
    COMPLIANCE --> WEB
    
    %% Data connections
    API --> DB
    API --> REDIS
    HOST --> STORAGE
    LIVEKIT --> REDIS
```

## デプロイメントアーキテクチャ

```mermaid
graph TB
    subgraph "Development"
        DEV[Local Development<br/>docker-compose]
        TEST[Testing<br/>GitHub Actions]
    end
    
    subgraph "CI/CD Pipeline"
        BUILD[Build Images<br/>Docker Build]
        SCAN[Security Scan<br/>Trivy, Gosec]
        TEST2[Integration Tests<br/>k6, Playwright]
        DEPLOY[Deploy to GCP<br/>Cloud Run]
    end
    
    subgraph "Production Environment"
        subgraph "GCP Services"
            CR[Cloud Run<br/>API, Web, Host, LiveKit]
            SQL[Cloud SQL<br/>PostgreSQL]
            REDIS[Redis<br/>Cache]
            STORAGE[Cloud Storage<br/>Backups]
        end
        
        subgraph "Monitoring"
            MONITOR[Cloud Monitoring]
            LOGS[Cloud Logging]
            ALERTS[Alerting Policies]
        end
    end
    
    subgraph "Deployment Strategies"
        BG[Blue-Green<br/>Deployment]
        CANARY[Canary<br/>Deployment]
        ROLLBACK[Rollback<br/>Strategy]
    end
    
    %% Development flow
    DEV --> TEST
    TEST --> BUILD
    
    %% CI/CD flow
    BUILD --> SCAN
    SCAN --> TEST2
    TEST2 --> DEPLOY
    
    %% Deployment flow
    DEPLOY --> BG
    DEPLOY --> CANARY
    BG --> ROLLBACK
    CANARY --> ROLLBACK
    
    %% Production flow
    ROLLBACK --> CR
    CR --> SQL
    CR --> REDIS
    CR --> STORAGE
    
    %% Monitoring flow
    CR --> MONITOR
    SQL --> LOGS
    REDIS --> ALERTS
    STORAGE --> MONITOR
```

## スケーリング戦略

```mermaid
graph TB
    subgraph "Auto Scaling"
        subgraph "Horizontal Scaling"
            H1[API Service<br/>0-10 instances]
            H2[Web Service<br/>0-5 instances]
            H3[LiveKit Service<br/>0-3 instances]
        end
        
        subgraph "Vertical Scaling"
            V1[Host Service<br/>Fixed 1 instance]
            V2[Database<br/>Manual scaling]
            V3[Redis<br/>Manual scaling]
        end
        
        subgraph "Load Balancing"
            LB1[Cloud Load Balancer]
            LB2[Traffic Distribution]
            LB3[Health Checks]
        end
    end
    
    subgraph "Scaling Triggers"
        CPU[CPU Usage > 70%]
        MEMORY[Memory Usage > 80%]
        REQUESTS[Request Rate > 1000/min]
        LATENCY[Latency > 500ms]
    end
    
    subgraph "Scaling Actions"
        SCALE_UP[Scale Up Instances]
        SCALE_DOWN[Scale Down Instances]
        ALERT[Send Alerts]
        LOG[Log Scaling Events]
    end
    
    %% Trigger flow
    CPU --> SCALE_UP
    MEMORY --> SCALE_UP
    REQUESTS --> SCALE_UP
    LATENCY --> SCALE_UP
    
    %% Action flow
    SCALE_UP --> H1
    SCALE_UP --> H2
    SCALE_UP --> H3
    SCALE_DOWN --> H1
    SCALE_DOWN --> H2
    SCALE_DOWN --> H3
    
    %% Load balancing
    LB1 --> LB2
    LB2 --> LB3
    LB3 --> H1
    LB3 --> H2
    LB3 --> H3
    
    %% Monitoring
    SCALE_UP --> ALERT
    SCALE_DOWN --> ALERT
    ALERT --> LOG
```

## 監視・ログアーキテクチャ

```mermaid
graph TB
    subgraph "Application Services"
        API[API Service]
        HOST[Host Service]
        LIVEKIT[LiveKit Service]
        WEB[Web Service]
    end
    
    subgraph "Data Sources"
        METRICS[Application Metrics]
        LOGS[Application Logs]
        TRACES[Distributed Traces]
        EVENTS[Custom Events]
    end
    
    subgraph "Collection Layer"
        AGENT[Cloud Monitoring Agent]
        LOGGING[Cloud Logging]
        TRACING[Cloud Trace]
        CUSTOM[Custom Metrics]
    end
    
    subgraph "Processing Layer"
        AGGREGATION[Metric Aggregation]
        FILTERING[Log Filtering]
        CORRELATION[Trace Correlation]
        ENRICHMENT[Event Enrichment]
    end
    
    subgraph "Storage Layer"
        METRIC_STORE[Metric Store]
        LOG_STORE[Log Store]
        TRACE_STORE[Trace Store]
        EVENT_STORE[Event Store]
    end
    
    subgraph "Analysis Layer"
        DASHBOARDS[Dashboards]
        ALERTS[Alerting]
        REPORTS[Reports]
        ANALYTICS[Analytics]
    end
    
    %% Data flow
    API --> METRICS
    HOST --> LOGS
    LIVEKIT --> TRACES
    WEB --> EVENTS
    
    %% Collection flow
    METRICS --> AGENT
    LOGS --> LOGGING
    TRACES --> TRACING
    EVENTS --> CUSTOM
    
    %% Processing flow
    AGENT --> AGGREGATION
    LOGGING --> FILTERING
    TRACING --> CORRELATION
    CUSTOM --> ENRICHMENT
    
    %% Storage flow
    AGGREGATION --> METRIC_STORE
    FILTERING --> LOG_STORE
    CORRELATION --> TRACE_STORE
    ENRICHMENT --> EVENT_STORE
    
    %% Analysis flow
    METRIC_STORE --> DASHBOARDS
    LOG_STORE --> ALERTS
    TRACE_STORE --> REPORTS
    EVENT_STORE --> ANALYTICS
```

## 災害復旧アーキテクチャ

```mermaid
graph TB
    subgraph "Primary Region"
        subgraph "Production Services"
            API1[API Service]
            HOST1[Host Service]
            LIVEKIT1[LiveKit Service]
            WEB1[Web Service]
        end
        
        subgraph "Data Stores"
            DB1[(Primary Database)]
            REDIS1[(Primary Redis)]
            STORAGE1[(Primary Storage)]
        end
    end
    
    subgraph "Backup Region"
        subgraph "Standby Services"
            API2[API Service]
            HOST2[Host Service]
            LIVEKIT2[LiveKit Service]
            WEB2[Web Service]
        end
        
        subgraph "Backup Data"
            DB2[(Backup Database)]
            REDIS2[(Backup Redis)]
            STORAGE2[(Backup Storage)]
        end
    end
    
    subgraph "Disaster Recovery"
        BACKUP[Automated Backups]
        REPLICATION[Data Replication]
        FAILOVER[Automatic Failover]
        RESTORE[Point-in-Time Restore]
    end
    
    subgraph "Recovery Procedures"
        DETECTION[Failure Detection]
        NOTIFICATION[Alert Notification]
        SWITCHOVER[Service Switchover]
        VALIDATION[Recovery Validation]
    end
    
    %% Primary region flow
    API1 --> DB1
    HOST1 --> REDIS1
    LIVEKIT1 --> STORAGE1
    WEB1 --> API1
    
    %% Backup region flow
    API2 --> DB2
    HOST2 --> REDIS2
    LIVEKIT2 --> STORAGE2
    WEB2 --> API2
    
    %% Disaster recovery flow
    DB1 --> BACKUP
    REDIS1 --> REPLICATION
    STORAGE1 --> FAILOVER
    BACKUP --> RESTORE
    
    %% Recovery procedures flow
    BACKUP --> DETECTION
    REPLICATION --> NOTIFICATION
    FAILOVER --> SWITCHOVER
    RESTORE --> VALIDATION
    
    %% Cross-region connections
    DB1 -.-> DB2
    REDIS1 -.-> REDIS2
    STORAGE1 -.-> STORAGE2
```

このアーキテクチャ図により、24時間AIラジオシステムの全体像と各コンポーネント間の関係を視覚的に理解できます。
