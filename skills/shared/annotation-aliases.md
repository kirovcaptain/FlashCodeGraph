## Annotation Aliases

When users describe annotations in natural language, map to the correct annotation name for `query_by_annotation`. The annotation name requires exact match — this alias table is essential for translation. The `params` filter supports substring match.

| User says | Annotation | Framework |
|-----------|-----------|-----------|
| xxl / xxljob / scheduled task | XxlJob | xxl-job |
| scheduled / cron (Spring) | Scheduled | Spring |
| cron (NestJS) | Cron | NestJS |
| rabbit / rabbitmq / rabbit listener | RabbitListener | RabbitMQ |
| kafka / kafka listener | KafkaListener | Kafka |
| rocketmq / rocket listener | RocketMQMessageListener | RocketMQ |
| event listener | EventListener | Spring |
| dubbo service / dubbo provider | DubboService | Dubbo |
| grpc service | GrpcService | gRPC |
| dubbo reference / dubbo client | DubboReference | Dubbo |
| transaction / transactional | Transactional | Spring |
| async / asynchronous | Async | Spring |
| cache / cacheable | Cacheable | Spring |
| cache evict | CacheEvict | Spring |
| auth / preauthorize | PreAuthorize | Spring Security |
| graphql query | QueryMapping | GraphQL |
| graphql mutation | MutationMapping | GraphQL |
| deprecated | Deprecated | common |
| test / unit test | Test | JUnit |
| mapper / mybatis | Mapper | MyBatis |

### Params Search: Word Splitting

The `params` filter is a substring match. When a compound keyword returns no results, split it into component words and retry with the shorter root word (at least 4 characters).

- "eazypay" → no results → retry with "eazy"
- "stripecallback" → no results → retry with "stripe"
