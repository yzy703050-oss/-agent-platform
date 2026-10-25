# 旅程安排逐次模型调用审计

2026-10-25，基于round6原始事件回看；本次未改提示词或产品代码，未重新消耗模型。此前12/12只证明原测试的保存数量判据，不能证明业务意图保留、调用效率或全部执行细节保真。

## 已确认的问题

- 三次多城分别3/7/8次实际模型调用。7和8次中均有3次select，后三次提交完全相同的selection_refs，保存修订从3到4到5；没有新车次/酒店查询。
- 3次调用样本先因business在政策前阶段不被允许而拒绝，第二次把前两段purpose改成visit，绕过政策流程。原请求明确为差旅，原测试没有检查purpose，不能把这一例当成业务语义保真通过。
- 8次样本含2次预检失败、1次政策调用决策、1次政策提取、1次建计划、1次首次选择、2次重复选择。失败两次17900tokens；重复两次24248tokens，合计42148/71551tokens。非必要分类依据本次真实轨迹，未宣称原Python一定无这些模型错误。
- 五份静态提示SHA与冻结原版一致，原Python93个文件SHA未变；这只能证明原文字节没有改变。另发现Go住宿能力缺口分类没有覆盖Python的酒店政策证据缺失分支，需复现后判定影响，不能直接当成本组三次重复的唯一根因。
- 7/8次样本的演示政策被标partial并含demo_policy_not_authoritative；选择后仍有policy_unverified及酒店政策/报价缺口，allow_repair一直为true，于是继续审查，最终靠无进展安全点收尾。

## 样本1：3次调用，13.45秒，27235tokens

原请求：请安排2026-10-26上海到杭州、10月27日杭州到南京、10月28日南京返回上海的三段往返差旅。杭州入住26日离店27日，南京入住27日离店28日，每地一晚。26日杭州14:00客户会议，27日南京14:00项目交流。优先高铁二等座，每段交通硬预算500元，总参考预算2500元为软偏好。保存完整行程，并查询和选择三段车次及两处酒店地点。演示房价、库存未知，不视作0元，不做预订付款；无需额外政策或网页检索。

最终purpose：visit, visit, return
实际供应方次数：{"hotel":2,"train":3}

| 次序 | 阶段 | 工具 | 模型耗时/秒 | 输入Token | 输出Token | 总Token |
| ---: | --- | --- | ---: | ---: | ---: | ---: |
| 1 | agent:main | write_trip | 5.52 | 6781 | 1477 | 8258 |
| 2 | agent:main | write_trip | 4.99 | 8082 | 1324 | 9406 |
| 3 | agent:trip_review | write_trip | 2.57 | 8994 | 577 | 9571 |

### 第1次：agent:main

工具：write_trip；call_id：call_00_ELupGVLCGavG3t9uCpdL3719
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "propose",
    "default_round_trip": false,
    "reason": "三段明确差旅：上海→杭州（26日14:00客户会议）、杭州→南京（27日14:00项目交流）、南京→上海返程；杭州26日一晚、南京27日一晚。",
    "confirmed_conditions": {
      "passengers": 1,
      "constraints": {
        "seat_class": "二等座",
        "train_max_total_cny": 500,
        "total_budget_cny": 2500
      }
    },
    "field_sources": {
      "passengers": "proposal",
      "seat_class": "user",
      "train_max_total_cny": "user",
      "total_budget_cny": "user",
      "train_kind": "proposal"
    },
    "condition_strengths": {
      "seat_class": "hard",
      "train_max_total_cny": "hard",
      "total_budget_cny": "soft"
    },
    "tasks": [
      {
        "origin": "上海",
        "destination": "杭州",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-26",
          "check_in": "2026-10-26",
          "check_out": "2026-10-27",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "arrival_before": "2026-10-26T13:30:00+08:00"
          }
        },
        "field_sources": {
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "arrival_before": "derived",
          "train_kind": "proposal"
        },
        "condition_strengths": {
          "departure_date": "hard",
          "check_in": "hard",
          "check_out": "hard",
          "arrival_before": "soft"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "上海",
              "destination": "杭州",
              "departure_date": "2026-10-26",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500,
              "arrival_before": "2026-10-26T13:30:00+08:00"
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "杭州",
              "check_in": "2026-10-26",
              "check_out": "2026-10-27",
              "guests": 1
            },
            "ranking": [
              {
                "field": "nightly_cny",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": [
          {
            "purpose": "客户会议",
            "description": "26日14:00在杭州参加客户会议。",
            "date": "2026-10-26",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "杭州",
        "destination": "南京",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-27",
          "check_in": "2026-10-27",
          "check_out": "2026-10-28",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "arrival_before": "2026-10-27T13:30:00+08:00"
          }
        },
        "field_sources": {
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "arrival_before": "derived",
          "train_kind": "proposal"
        },
        "condition_strengths": {
          "departure_date": "hard",
          "check_in": "hard",
          "check_out": "hard",
          "arrival_before": "soft"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "杭州",
              "destination": "南京",
              "departure_date": "2026-10-27",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500,
              "arrival_before": "2026-10-27T13:30:00+08:00"
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "南京",
              "check_in": "2026-10-27",
              "check_out": "2026-10-28",
              "guests": 1
            },
            "ranking": [
              {
                "field": "nightly_cny",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": [
          {
            "purpose": "项目交流",
            "description": "27日14:00在南京参加项目交流。",
            "date": "2026-10-27",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "南京",
        "destination": "上海",
        "purpose": "return",
        "requires_hotel": false,
        "conditions": {
          "departure_date": "2026-10-28",
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "departure_date": "user",
          "train_kind": "proposal"
        },
        "condition_strengths": {
          "departure_date": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "南京",
              "destination": "上海",
              "departure_date": "2026-10-28",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "departure_at",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": []
      }
    ]
  }
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "rejected",
  "operation": null,
  "trip_plan_id": null,
  "revision": null,
  "stop_reason": null,
  "stop_planning": null,
  "reason": "参数不符合当前工具结构。",
  "error": {
    "code": "INVALID_ARGUMENTS",
    "message": "参数不符合当前工具结构。",
    "details": {
      "fields": [
        {
          "field": "tripplan.tasks.*.purpose",
          "message": "此字段必须使用当前阶段允许的枚举：[\"visit\",\"return\",\"transit\",\"unspecified\"]",
          "type": "schema_validation"
        }
      ]
    }
  },
  "issues": null
}
```

### 第2次：agent:main

工具：write_trip；call_id：call_00_w5gYdw6Bpbl0SbwKISBh1768
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "propose",
    "reason": "三段明确差旅：上海→杭州（26日14:00客户会议）、杭州→南京（27日14:00项目交流）、南京→上海返程；杭州26日一晚、南京27日一晚。",
    "default_round_trip": false,
    "confirmed_conditions": {
      "passengers": 1,
      "constraints": {
        "seat_class": "二等座",
        "train_max_total_cny": 500,
        "total_budget_cny": 2500
      }
    },
    "field_sources": {
      "passengers": "proposal",
      "seat_class": "user",
      "train_max_total_cny": "user",
      "total_budget_cny": "user",
      "train_kind": "proposal"
    },
    "condition_strengths": {
      "seat_class": "hard",
      "train_max_total_cny": "hard",
      "total_budget_cny": "soft"
    },
    "tasks": [
      {
        "origin": "上海",
        "destination": "杭州",
        "purpose": "visit",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-26",
          "check_in": "2026-10-26",
          "check_out": "2026-10-27",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "train_kind": "proposal"
        },
        "condition_strengths": {
          "departure_date": "hard",
          "check_in": "hard",
          "check_out": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "上海",
              "destination": "杭州",
              "departure_date": "2026-10-26",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "杭州",
              "check_in": "2026-10-26",
              "check_out": "2026-10-27",
              "guests": 1
            },
            "ranking": [
              {
                "field": "nightly_cny",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": [
          {
            "date": "2026-10-26",
            "time": "14:00",
            "purpose": "客户会议",
            "description": "26日14:00在杭州参加客户会议。",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "杭州",
        "destination": "南京",
        "purpose": "visit",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-27",
          "check_in": "2026-10-27",
          "check_out": "2026-10-28",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "train_kind": "proposal"
        },
        "condition_strengths": {
          "departure_date": "hard",
          "check_in": "hard",
          "check_out": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "杭州",
              "destination": "南京",
              "departure_date": "2026-10-27",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "南京",
              "check_in": "2026-10-27",
              "check_out": "2026-10-28",
              "guests": 1
            },
            "ranking": [
              {
                "field": "nightly_cny",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": [
          {
            "date": "2026-10-27",
            "time": "14:00",
            "purpose": "项目交流",
            "description": "27日14:00在南京参加项目交流。",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "南京",
        "destination": "上海",
        "purpose": "return",
        "requires_hotel": false,
        "conditions": {
          "departure_date": "2026-10-28",
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "departure_date": "user",
          "train_kind": "proposal"
        },
        "condition_strengths": {
          "departure_date": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "南京",
              "destination": "上海",
              "departure_date": "2026-10-28",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "departure_at",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": []
      }
    ]
  }
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "partial",
  "operation": "propose",
  "trip_plan_id": "45b1a73c-ac46-462c-9f55-75d59f0956fb",
  "revision": 1,
  "stop_reason": "awaiting_model",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "train_pending",
      "task_id": "t1",
      "field": "train",
      "message": "尚未选择有效车次。",
      "blocking": true
    },
    {
      "code": "hotel_pending",
      "task_id": "t1",
      "field": "hotel",
      "message": "尚未选择酒店地点。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点不提供房价、空房或入住规则。",
      "blocking": false
    },
    {
      "code": "train_pending",
      "task_id": "t2",
      "field": "train",
      "message": "尚未选择有效车次。",
      "blocking": true
    },
    {
      "code": "hotel_pending",
      "task_id": "t2",
      "field": "hotel",
      "message": "尚未选择酒店地点。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点不提供房价、空房或入住规则。",
      "blocking": false
    },
    {
      "code": "train_pending",
      "task_id": "t3",
      "field": "train",
      "message": "尚未选择有效车次。",
      "blocking": true
    }
  ]
}
```

### 第3次：agent:trip_review

工具：write_trip；call_id：call_00_7FF22faY3it2aZDMBL1p5702
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "select",
    "trip_plan_id": "45b1a73c-ac46-462c-9f55-75d59f0956fb",
    "expected_revision": 2,
    "allow_repair": true,
    "reason": "选择三段高铁二等座及两处酒店地点",
    "tasks": [
      {
        "task_id": "t1",
        "expected_revision": 1,
        "components": [
          "train",
          "hotel",
          "content"
        ],
        "selection_refs": {
          "train": {
            "query_id": "ecb4cdeb-a949-4b63-9872-3db6d1ae8bc7",
            "result_revision": 1,
            "candidate_id": "demo-19502d92be43-0"
          },
          "hotel": {
            "query_id": "a954a735-999e-436c-879e-cbc3acd6e13d",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t2",
        "expected_revision": 1,
        "components": [
          "train",
          "hotel",
          "content"
        ],
        "selection_refs": {
          "train": {
            "query_id": "6b4fbdf8-fe26-4511-a1c5-af6b02e02429",
            "result_revision": 1,
            "candidate_id": "demo-71b63fde423c-0"
          },
          "hotel": {
            "query_id": "c3535424-9a43-43ce-a745-923fe20229ba",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t3",
        "expected_revision": 1,
        "components": [
          "train"
        ],
        "selection_refs": {
          "train": {
            "query_id": "f1b85612-f0e6-4adf-b667-e2208efdd5c5",
            "result_revision": 1,
            "candidate_id": "demo-cae1e83795fa-0"
          }
        }
      }
    ]
  },
  "reply": {},
  "round_summary": "三段高铁与两处酒店地点均已选定：26日上海至杭州上午抵达，27日杭州至南京上午抵达，28日南京返上海；两晚住宿覆盖实际过夜。"
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "completed",
  "operation": "select",
  "trip_plan_id": "45b1a73c-ac46-462c-9f55-75d59f0956fb",
  "revision": 3,
  "stop_reason": "",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点不提供房价、空房或入住规则。",
      "blocking": false
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点不提供房价、空房或入住规则。",
      "blocking": false
    }
  ]
}
```

### 最终输出

```text
演示数据，仅用于展示，不代表真实车次、报价或房态。

已保存以下安排：

上海→杭州：
演示 G1001，2026-10-26 09:00出发，2026-10-26 11:00抵达，二等座，100元。
住宿：杭州演示酒店地点，2026-10-26入住、2026-10-27退房；房价与房态未核实。
活动：2026-10-26 14:00 客户会议；26日14:00在杭州参加客户会议。。

杭州→南京：
演示 G1001，2026-10-27 09:00出发，2026-10-27 11:00抵达，二等座，100元。
住宿：南京演示酒店地点，2026-10-27入住、2026-10-28退房；房价与房态未核实。
活动：2026-10-27 14:00 项目交流；27日14:00在南京参加项目交流。。

南京→上海：
演示 G1001，2026-10-28 09:00出发，2026-10-28 11:00抵达，二等座，100元。

交通小计：300元（不含住宿）。
```

## 样本2：7次调用，23.36秒，61163tokens

原请求：请安排2026-10-26上海到杭州、10月27日杭州到南京、10月28日南京返回上海的三段往返差旅。杭州入住26日离店27日，南京入住27日离店28日，每地一晚。26日杭州14:00客户会议，27日南京14:00项目交流。优先高铁二等座，每段交通硬预算500元，总参考预算2500元为软偏好。保存完整行程，并查询和选择三段车次及两处酒店地点。演示房价、库存未知，不视作0元，不做预订付款；无需额外政策或网页检索。

最终purpose：business, business, return
实际供应方次数：{"hotel":2,"train":3}

| 次序 | 阶段 | 工具 | 模型耗时/秒 | 输入Token | 输出Token | 总Token |
| ---: | --- | --- | ---: | ---: | ---: | ---: |
| 1 | agent:main | write_trip | 5.13 | 6781 | 1407 | 8188 |
| 2 | agent:main | query_policy | 1.23 | 8011 | 75 | 8086 |
| 3 | agent:travel_policy |  | 2.31 | 826 | 522 | 1348 |
| 4 | agent:main | write_trip | 4.66 | 7178 | 1365 | 8543 |
| 5 | agent:trip_review | write_trip | 2.92 | 10155 | 640 | 10795 |
| 6 | agent:trip_review | write_trip | 3.02 | 11247 | 590 | 11837 |
| 7 | agent:trip_review | write_trip | 3.52 | 11718 | 648 | 12366 |

### 第1次：agent:main

工具：write_trip；call_id：call_00_f4CR6gcVdb4oUtRuzKK40686
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "propose",
    "default_round_trip": false,
    "reason": "用户明确三段往返差旅：上海→杭州→南京→上海，含两处一晚住宿与两场14:00客户/项目活动",
    "confirmed_conditions": {
      "passengers": 1,
      "guests": 1,
      "constraints": {
        "seat_class": "二等座",
        "train_kind": "high_speed",
        "train_max_total_cny": 500,
        "total_budget_cny": 2500
      }
    },
    "field_sources": {
      "passengers": "context",
      "guests": "context",
      "seat_class": "user",
      "train_kind": "proposal",
      "train_max_total_cny": "user",
      "total_budget_cny": "user"
    },
    "condition_strengths": {
      "seat_class": "hard",
      "train_max_total_cny": "hard",
      "total_budget_cny": "soft"
    },
    "tasks": [
      {
        "origin": "上海",
        "destination": "杭州",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-26",
          "check_in": "2026-10-26",
          "check_out": "2026-10-27",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "arrival_before": "2026-10-26T13:30:00+08:00"
          }
        },
        "field_sources": {
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "derived",
          "arrival_before": "derived"
        },
        "condition_strengths": {
          "departure_date": "hard",
          "check_in": "hard",
          "check_out": "hard",
          "arrival_before": "soft"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "上海",
              "destination": "杭州",
              "departure_date": "2026-10-26",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "杭州",
              "check_in": "2026-10-26",
              "check_out": "2026-10-27",
              "guests": 1
            },
            "ranking": [
              {
                "field": "nightly_cny",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": [
          {
            "purpose": "客户会议",
            "description": "14:00 杭州客户会议",
            "date": "2026-10-26",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "杭州",
        "destination": "南京",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-27",
          "check_in": "2026-10-27",
          "check_out": "2026-10-28",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "arrival_before": "2026-10-27T13:30:00+08:00"
          }
        },
        "field_sources": {
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "derived",
          "arrival_before": "derived"
        },
        "condition_strengths": {
          "departure_date": "hard",
          "check_in": "hard",
          "check_out": "hard",
          "arrival_before": "soft"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "杭州",
              "destination": "南京",
              "departure_date": "2026-10-27",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "南京",
              "check_in": "2026-10-27",
              "check_out": "2026-10-28",
              "guests": 1
            },
            "ranking": [
              {
                "field": "nightly_cny",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": [
          {
            "purpose": "项目交流",
            "description": "14:00 南京项目交流",
            "date": "2026-10-27",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "南京",
        "destination": "上海",
        "purpose": "return",
        "requires_hotel": false,
        "conditions": {
          "departure_date": "2026-10-28",
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "departure_date": "user"
        },
        "condition_strengths": {
          "departure_date": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "南京",
              "destination": "上海",
              "departure_date": "2026-10-28",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "departure_at",
                "direction": "desc"
              }
            ]
          }
        ],
        "content": []
      }
    ]
  }
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "rejected",
  "operation": null,
  "trip_plan_id": null,
  "revision": null,
  "stop_reason": null,
  "stop_planning": null,
  "reason": "参数不符合当前工具结构。",
  "error": {
    "code": "INVALID_ARGUMENTS",
    "message": "参数不符合当前工具结构。",
    "details": {
      "fields": [
        {
          "field": "tripplan.tasks.*.purpose",
          "message": "此字段必须使用当前阶段允许的枚举：[\"visit\",\"return\",\"transit\",\"unspecified\"]",
          "type": "schema_validation"
        }
      ]
    }
  },
  "issues": null
}
```

### 第2次：agent:main

工具：query_policy；call_id：call_00_v937JdYSGaambS1GoE2Z5438
```json
{
  "request_kind": "planning",
  "context": {
    "note": "用户明确为三段往返差旅，需企业差旅政策约束"
  }
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "query_policy",
  "status": "partial",
  "operation": null,
  "trip_plan_id": null,
  "revision": null,
  "stop_reason": null,
  "stop_planning": null,
  "reason": null,
  "error": null,
  "issues": null
}
```

### 第3次：agent:travel_policy

政策工具内部的真实模型提取调用，不是一次新的Main决策。下列为政策工具最终返回：
```json
{
  "call_id": "call_00_v937JdYSGaambS1GoE2Z5438",
  "name": "query_policy",
  "status": "partial",
  "data": {
    "status": "partial",
    "summary": "演示政策：依据演示政策，三段高铁均限二等座且单程交通演示额度不超过500元；杭州、南京住宿演示额度均不超过400元/晚。未知房价和库存保留未核验状态，不视作0元，不宣称正式报销合规。",
    "constraints": [
      {
        "id": "train_seat_class",
        "rule": "高铁二等座",
        "applicability": {},
        "hard": true,
        "evidence_ids": [
          "demo-policy"
        ],
        "field": "seat_class",
        "operator": "==",
        "value": "二等座",
        "unit": null
      },
      {
        "id": "train_max_total_cny_per_segment",
        "rule": "单程交通演示额度不超过500元",
        "applicability": {},
        "hard": true,
        "evidence_ids": [
          "demo-policy"
        ],
        "field": "train_max_total_cny",
        "operator": "<=",
        "value": 500,
        "unit": "CNY"
      },
      {
        "id": "hotel_max_nightly_cny_hangzhou",
        "rule": "杭州住宿演示额度不超过400元/晚",
        "applicability": {
          "city": "杭州"
        },
        "hard": true,
        "evidence_ids": [
          "demo-policy"
        ],
        "field": "hotel_max_nightly_cny",
        "operator": "<=",
        "value": 400,
        "unit": "CNY/night"
      },
      {
        "id": "hotel_max_nightly_cny_nanjing",
        "rule": "南京住宿演示额度不超过400元/晚",
        "applicability": {
          "city": "南京"
        },
        "hard": true,
        "evidence_ids": [
          "demo-policy"
        ],
        "field": "hotel_max_nightly_cny",
        "operator": "<=",
        "value": 400,
        "unit": "CNY/night"
      }
    ],
    "applicability": {},
    "references": [
      {
        "id": "demo-policy",
        "source": "demo-travel-policy.txt"
      }
    ],
    "effective_date": null,
    "missing_fields": [
      "policy_applicability:city",
      "demo_policy_not_authoritative"
    ],
    "conflicts": []
  }
}
```

### 第4次：agent:main

工具：write_trip；call_id：call_00_qgJof04TcGtGWznbaNdK3266
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "propose",
    "reason": "按三段差旅与两晚住宿生成查询地图",
    "default_round_trip": false,
    "confirmed_conditions": {
      "passengers": 1,
      "guests": 1,
      "constraints": {
        "seat_class": "二等座",
        "train_max_total_cny": 500,
        "total_budget_cny": 2500
      }
    },
    "field_sources": {
      "passengers": "proposal",
      "guests": "proposal",
      "seat_class": "user",
      "train_max_total_cny": "user",
      "total_budget_cny": "user"
    },
    "condition_strengths": {
      "seat_class": "hard",
      "train_max_total_cny": "hard",
      "total_budget_cny": "soft"
    },
    "tasks": [
      {
        "origin": "上海",
        "destination": "杭州",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-26",
          "arrival_before": "2026-10-26T13:00:00+08:00",
          "check_in": "2026-10-26",
          "check_out": "2026-10-27",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "hotel_max_nightly_cny": 400
          }
        },
        "field_sources": {
          "arrival_before": "derived",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "hotel_max_nightly_cny": "derived",
          "train_kind": "proposal"
        },
        "condition_strengths": {
          "arrival_before": "soft",
          "hotel_max_nightly_cny": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "上海",
              "destination": "杭州",
              "departure_date": "2026-10-26",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "杭州",
              "check_in": "2026-10-26",
              "check_out": "2026-10-27",
              "guests": 1
            },
            "constraints": {
              "hotel_max_nightly_cny": 400
            }
          }
        ],
        "content": [
          {
            "purpose": "客户会议",
            "description": "14:00 杭州客户会议",
            "date": "2026-10-26",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "杭州",
        "destination": "南京",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-27",
          "arrival_before": "2026-10-27T13:00:00+08:00",
          "check_in": "2026-10-27",
          "check_out": "2026-10-28",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "hotel_max_nightly_cny": 400
          }
        },
        "field_sources": {
          "arrival_before": "derived",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "hotel_max_nightly_cny": "derived",
          "train_kind": "proposal"
        },
        "condition_strengths": {
          "arrival_before": "soft",
          "hotel_max_nightly_cny": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "杭州",
              "destination": "南京",
              "departure_date": "2026-10-27",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "南京",
              "check_in": "2026-10-27",
              "check_out": "2026-10-28",
              "guests": 1
            },
            "constraints": {
              "hotel_max_nightly_cny": 400
            }
          }
        ],
        "content": [
          {
            "purpose": "项目交流",
            "description": "14:00 南京项目交流",
            "date": "2026-10-27",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "南京",
        "destination": "上海",
        "purpose": "return",
        "requires_hotel": false,
        "conditions": {
          "departure_date": "2026-10-28",
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "train_kind": "proposal"
        },
        "condition_strengths": {},
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "南京",
              "destination": "上海",
              "departure_date": "2026-10-28",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "departure_at",
                "direction": "desc"
              }
            ]
          }
        ],
        "content": []
      }
    ]
  }
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "partial",
  "operation": "propose",
  "trip_plan_id": "cc0e79d9-42fe-457a-b05a-0caaeb7a102f",
  "revision": 1,
  "stop_reason": "awaiting_model",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "train_pending",
      "task_id": "t1",
      "field": "train",
      "message": "尚未选择有效车次。",
      "blocking": true
    },
    {
      "code": "hotel_pending",
      "task_id": "t1",
      "field": "hotel",
      "message": "尚未选择酒店地点。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "train_pending",
      "task_id": "t2",
      "field": "train",
      "message": "尚未选择有效车次。",
      "blocking": true
    },
    {
      "code": "hotel_pending",
      "task_id": "t2",
      "field": "hotel",
      "message": "尚未选择酒店地点。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "train_pending",
      "task_id": "t3",
      "field": "train",
      "message": "尚未选择有效车次。",
      "blocking": true
    },
    {
      "code": "policy_unverified",
      "field": "policy",
      "message": "差旅政策尚未核验。",
      "blocking": true
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "train_seat_class"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "train_seat_class"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t3",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t3"
      ],
      "evidence": "train_seat_class"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "train_max_total_cny_per_segment"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "train_max_total_cny_per_segment"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t3",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t3"
      ],
      "evidence": "train_max_total_cny_per_segment"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "hotel_max_nightly_cny_hangzhou"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "hotel_max_nightly_cny_nanjing"
    }
  ]
}
```

### 第5次：agent:trip_review

工具：write_trip；call_id：call_00_IuuNNvhfSDOd8r20UaS39056
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "select",
    "trip_plan_id": "cc0e79d9-42fe-457a-b05a-0caaeb7a102f",
    "expected_revision": 2,
    "allow_repair": true,
    "reason": "选择三段高铁与两处酒店地点",
    "tasks": [
      {
        "task_id": "t1",
        "expected_revision": 1,
        "components": [
          "train",
          "hotel",
          "content"
        ],
        "selection_refs": {
          "train": {
            "query_id": "abdfc1de-2ac2-4975-95f5-2ccc8cb961aa",
            "result_revision": 1,
            "candidate_id": "demo-19502d92be43-0"
          },
          "hotel": {
            "query_id": "905886d5-4379-440e-9ff7-cdf6fe3c25d1",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t2",
        "expected_revision": 1,
        "components": [
          "train",
          "hotel",
          "content"
        ],
        "selection_refs": {
          "train": {
            "query_id": "14e1d2c8-aad6-4812-821c-3085b3798e17",
            "result_revision": 1,
            "candidate_id": "demo-71b63fde423c-0"
          },
          "hotel": {
            "query_id": "7e910bc0-fc7e-4445-9658-831cd7691e6d",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t3",
        "expected_revision": 1,
        "components": [
          "train"
        ],
        "selection_refs": {
          "train": {
            "query_id": "58178a35-a31f-40a0-b2d8-57b12d2b5b23",
            "result_revision": 1,
            "candidate_id": "demo-cae1e83795fa-1"
          }
        }
      }
    ]
  },
  "reply": {
    "tasks": {
      "t1": [
        "train",
        "hotel",
        "content"
      ],
      "t2": [
        "train",
        "hotel",
        "content"
      ],
      "t3": [
        "train"
      ]
    },
    "explanation": "三段高铁均为演示二等座车次，两处酒店仅确认地点，房价与库存未核实，未做预订付款。"
  },
  "round_summary": "选定三段高铁及杭州、南京两处酒店地点；返程取较晚车次，避免与南京行程冲突。"
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "partial",
  "operation": "select",
  "trip_plan_id": "cc0e79d9-42fe-457a-b05a-0caaeb7a102f",
  "revision": 3,
  "stop_reason": "awaiting_model",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "policy_unverified",
      "field": "policy",
      "message": "差旅政策尚未核验。",
      "blocking": true
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "hotel_max_nightly_cny_hangzhou"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "hotel_max_nightly_cny_nanjing"
    }
  ]
}
```

### 第6次：agent:trip_review

工具：write_trip；call_id：call_00_UQ5jR752FnaRupTTSncr9491
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "select",
    "trip_plan_id": "cc0e79d9-42fe-457a-b05a-0caaeb7a102f",
    "expected_revision": 3,
    "allow_repair": true,
    "reason": "确认三段高铁与两处酒店地点选择，保留演示数据未核验状态。",
    "tasks": [
      {
        "task_id": "t1",
        "expected_revision": 1,
        "components": [
          "train",
          "hotel",
          "content"
        ],
        "selection_refs": {
          "train": {
            "query_id": "abdfc1de-2ac2-4975-95f5-2ccc8cb961aa",
            "result_revision": 1,
            "candidate_id": "demo-19502d92be43-0"
          },
          "hotel": {
            "query_id": "905886d5-4379-440e-9ff7-cdf6fe3c25d1",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t2",
        "expected_revision": 1,
        "components": [
          "train",
          "hotel",
          "content"
        ],
        "selection_refs": {
          "train": {
            "query_id": "14e1d2c8-aad6-4812-821c-3085b3798e17",
            "result_revision": 1,
            "candidate_id": "demo-71b63fde423c-0"
          },
          "hotel": {
            "query_id": "7e910bc0-fc7e-4445-9658-831cd7691e6d",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t3",
        "expected_revision": 1,
        "components": [
          "train"
        ],
        "selection_refs": {
          "train": {
            "query_id": "58178a35-a31f-40a0-b2d8-57b12d2b5b23",
            "result_revision": 1,
            "candidate_id": "demo-cae1e83795fa-1"
          }
        }
      }
    ]
  },
  "reply": {},
  "round_summary": "核对三段车次与两晚住宿覆盖后确认选择：26日上海至杭州、27日杭州至南京、28日南京返上海，杭州与南京各一晚。"
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "partial",
  "operation": "select",
  "trip_plan_id": "cc0e79d9-42fe-457a-b05a-0caaeb7a102f",
  "revision": 4,
  "stop_reason": "awaiting_model",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "policy_unverified",
      "field": "policy",
      "message": "差旅政策尚未核验。",
      "blocking": true
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "hotel_max_nightly_cny_hangzhou"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "hotel_max_nightly_cny_nanjing"
    }
  ]
}
```

### 第7次：agent:trip_review

工具：write_trip；call_id：call_00_b0hBYwFxPwpEeIwn7JR35356
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "select",
    "trip_plan_id": "cc0e79d9-42fe-457a-b05a-0caaeb7a102f",
    "expected_revision": 4,
    "allow_repair": true,
    "reason": "三段车次与两晚住宿覆盖完整，确认选择。",
    "tasks": [
      {
        "task_id": "t1",
        "expected_revision": 1,
        "components": [
          "train",
          "hotel",
          "content"
        ],
        "selection_refs": {
          "train": {
            "query_id": "abdfc1de-2ac2-4975-95f5-2ccc8cb961aa",
            "result_revision": 1,
            "candidate_id": "demo-19502d92be43-0"
          },
          "hotel": {
            "query_id": "905886d5-4379-440e-9ff7-cdf6fe3c25d1",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t2",
        "expected_revision": 1,
        "components": [
          "train",
          "hotel",
          "content"
        ],
        "selection_refs": {
          "train": {
            "query_id": "14e1d2c8-aad6-4812-821c-3085b3798e17",
            "result_revision": 1,
            "candidate_id": "demo-71b63fde423c-0"
          },
          "hotel": {
            "query_id": "7e910bc0-fc7e-4445-9658-831cd7691e6d",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t3",
        "expected_revision": 1,
        "components": [
          "train"
        ],
        "selection_refs": {
          "train": {
            "query_id": "58178a35-a31f-40a0-b2d8-57b12d2b5b23",
            "result_revision": 1,
            "candidate_id": "demo-cae1e83795fa-1"
          }
        }
      }
    ]
  },
  "reply": {
    "tasks": {
      "t1": [
        "train",
        "hotel",
        "content"
      ],
      "t2": [
        "train",
        "hotel",
        "content"
      ],
      "t3": [
        "train"
      ]
    },
    "explanation": "三段高铁均为二等座且单程票价在硬预算内，两处住宿各覆盖一晚；演示房价与库存未知，未做预订付款。"
  },
  "round_summary": "确认三段高铁与杭州、南京各一晚住宿地点，返程取较晚车次以避开南京行程。"
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "partial",
  "operation": "select",
  "trip_plan_id": "cc0e79d9-42fe-457a-b05a-0caaeb7a102f",
  "revision": 5,
  "stop_reason": "awaiting_model",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "policy_unverified",
      "field": "policy",
      "message": "差旅政策尚未核验。",
      "blocking": true
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "hotel_max_nightly_cny_hangzhou"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "hotel_max_nightly_cny_nanjing"
    }
  ]
}
```

### 最终输出

```text
演示数据，仅用于展示，不代表真实车次、报价或房态。

已保留以下安排，仍有待处理问题：

上海→杭州：
演示 G1001，2026-10-26 09:00出发，2026-10-26 11:00抵达，二等座，100元。
住宿：杭州演示酒店地点，2026-10-26入住、2026-10-27退房；房价与房态未核实。
活动：2026-10-26 14:00 客户会议；14:00 杭州客户会议。

杭州→南京：
演示 G1001，2026-10-27 09:00出发，2026-10-27 11:00抵达，二等座，100元。
住宿：南京演示酒店地点，2026-10-27入住、2026-10-28退房；房价与房态未核实。
活动：2026-10-27 14:00 项目交流；14:00 南京项目交流。

南京→上海：
演示 G1002，2026-10-28 14:00出发，2026-10-28 16:00抵达，二等座，150元。

交通小计：350元（不含住宿）。

企业政策仍有未核实事项，不能确认合规完成。

待处理：酒店地点信息不包含房价和库存。
```

## 样本3：8次调用，28.27秒，71551tokens

原请求：请安排2026-10-26上海到杭州、10月27日杭州到南京、10月28日南京返回上海的三段往返差旅。杭州入住26日离店27日，南京入住27日离店28日，每地一晚。26日杭州14:00客户会议，27日南京14:00项目交流。优先高铁二等座，每段交通硬预算500元，总参考预算2500元为软偏好。保存完整行程，并查询和选择三段车次及两处酒店地点。演示房价、库存未知，不视作0元，不做预订付款；无需额外政策或网页检索。

最终purpose：business, business, return
实际供应方次数：{"hotel":2,"train":3}

| 次序 | 阶段 | 工具 | 模型耗时/秒 | 输入Token | 输出Token | 总Token |
| ---: | --- | --- | ---: | ---: | ---: | ---: |
| 1 | agent:main | read, write_trip | 5.28 | 6781 | 1383 | 8164 |
| 2 | agent:main | write_trip | 4.66 | 8350 | 1386 | 9736 |
| 3 | agent:main | query_policy | 1.55 | 8173 | 164 | 8337 |
| 4 | agent:travel_policy |  | 1.96 | 893 | 351 | 1244 |
| 5 | agent:main | write_trip | 5.58 | 7337 | 1535 | 8872 |
| 6 | agent:trip_review | write_trip | 3.51 | 10390 | 560 | 10950 |
| 7 | agent:trip_review | write_trip | 2.64 | 11418 | 506 | 11924 |
| 8 | agent:trip_review | write_trip | 2.44 | 11806 | 518 | 12324 |

### 第1次：agent:main

工具：read；call_id：call_00_9c7hVnE4fgiBzYPHcYxv4586
```json
{
  "request_kind": "planning",
  "resource": "trip_summaries"
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "read",
  "status": "rejected",
  "operation": null,
  "trip_plan_id": null,
  "revision": null,
  "stop_reason": null,
  "stop_planning": null,
  "reason": "参数不符合当前工具结构。",
  "error": {
    "code": "INVALID_ARGUMENTS",
    "message": "参数不符合当前工具结构。",
    "details": {
      "fields": [
        {
          "field": "tripplan.tasks.*.conditions.constraints",
          "message": "此对象不允许额外字段；允许的字段为：[\"available_only\",\"departure_time_after\",\"departure_time_before\",\"hotel_max_nightly_cny\",\"hotel_max_total_cny\",\"seat_class\",\"total_budget_cny\",\"train_kind\",\"train_max_total_cny\"]",
          "type": "schema_validation"
        }
      ]
    }
  },
  "issues": null
}
```

工具：write_trip；call_id：call_01_f8mKvz8v4P6VHkYm1kvu0737
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "propose",
    "default_round_trip": false,
    "reason": "用户明确三段往返差旅：上海→杭州、杭州→南京、南京→上海，各含硬性会议/交流时间",
    "confirmed_conditions": {
      "passengers": 1,
      "constraints": {
        "seat_class": "二等座",
        "train_kind": "high_speed",
        "train_max_total_cny": 500,
        "total_budget_cny": 2500
      }
    },
    "field_sources": {
      "passengers": "proposal",
      "seat_class": "user",
      "train_kind": "user",
      "train_max_total_cny": "user",
      "total_budget_cny": "user"
    },
    "condition_strengths": {
      "seat_class": "hard",
      "train_kind": "hard",
      "train_max_total_cny": "hard",
      "total_budget_cny": "soft"
    },
    "tasks": [
      {
        "origin": "上海",
        "destination": "杭州",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "start_date": "2026-10-26",
          "departure_date": "2026-10-26",
          "check_in": "2026-10-26",
          "check_out": "2026-10-27",
          "nights": 1,
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "arrival_before": "2026-10-26T13:30:00+08:00"
          }
        },
        "field_sources": {
          "start_date": "user",
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "arrival_before": "derived"
        },
        "condition_strengths": {
          "arrival_before": "soft"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "上海",
              "destination": "杭州",
              "departure_date": "2026-10-26",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "杭州",
              "check_in": "2026-10-26",
              "check_out": "2026-10-27",
              "guests": 1
            }
          }
        ],
        "content": [
          {
            "purpose": "客户会议",
            "description": "杭州客户会议，14:00开始",
            "date": "2026-10-26",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "杭州",
        "destination": "南京",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "start_date": "2026-10-27",
          "departure_date": "2026-10-27",
          "check_in": "2026-10-27",
          "check_out": "2026-10-28",
          "nights": 1,
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "arrival_before": "2026-10-27T13:30:00+08:00"
          }
        },
        "field_sources": {
          "start_date": "user",
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "arrival_before": "derived"
        },
        "condition_strengths": {
          "arrival_before": "soft"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "杭州",
              "destination": "南京",
              "departure_date": "2026-10-27",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "南京",
              "check_in": "2026-10-27",
              "check_out": "2026-10-28",
              "guests": 1
            }
          }
        ],
        "content": [
          {
            "purpose": "项目交流",
            "description": "南京项目交流，14:00开始",
            "date": "2026-10-27",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "南京",
        "destination": "上海",
        "purpose": "return",
        "requires_hotel": false,
        "conditions": {
          "start_date": "2026-10-28",
          "departure_date": "2026-10-28",
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "start_date": "user",
          "departure_date": "user"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "南京",
              "destination": "上海",
              "departure_date": "2026-10-28",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "departure_at",
                "direction": "desc"
              }
            ]
          }
        ],
        "content": []
      }
    ]
  }
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "rejected",
  "operation": null,
  "trip_plan_id": null,
  "revision": null,
  "stop_reason": null,
  "stop_planning": null,
  "reason": "参数不符合当前工具结构。",
  "error": {
    "code": "INVALID_ARGUMENTS",
    "message": "参数不符合当前工具结构。",
    "details": {
      "fields": [
        {
          "field": "tripplan.tasks.*.conditions.constraints",
          "message": "此对象不允许额外字段；允许的字段为：[\"available_only\",\"departure_time_after\",\"departure_time_before\",\"hotel_max_nightly_cny\",\"hotel_max_total_cny\",\"seat_class\",\"total_budget_cny\",\"train_kind\",\"train_max_total_cny\"]",
          "type": "schema_validation"
        }
      ]
    }
  },
  "issues": null
}
```

### 第2次：agent:main

工具：write_trip；call_id：call_00_NWGQIaFIm19xmqEyCJvw1561
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "propose",
    "reason": "用户明确三段往返差旅：上海→杭州、杭州→南京、南京→上海，各含硬性会议/交流时间",
    "default_round_trip": false,
    "confirmed_conditions": {
      "passengers": 1,
      "constraints": {
        "seat_class": "二等座",
        "train_kind": "high_speed",
        "train_max_total_cny": 500,
        "total_budget_cny": 2500
      }
    },
    "field_sources": {
      "passengers": "proposal",
      "seat_class": "user",
      "train_kind": "user",
      "train_max_total_cny": "user",
      "total_budget_cny": "user"
    },
    "condition_strengths": {
      "seat_class": "hard",
      "train_kind": "hard",
      "train_max_total_cny": "hard",
      "total_budget_cny": "soft"
    },
    "tasks": [
      {
        "origin": "上海",
        "destination": "杭州",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "start_date": "2026-10-26",
          "departure_date": "2026-10-26",
          "check_in": "2026-10-26",
          "check_out": "2026-10-27",
          "nights": 1,
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "start_date": "user",
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "seat_class": "user",
          "train_kind": "user",
          "train_max_total_cny": "user"
        },
        "condition_strengths": {
          "seat_class": "hard",
          "train_kind": "hard",
          "train_max_total_cny": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "上海",
              "destination": "杭州",
              "departure_date": "2026-10-26",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "杭州",
              "check_in": "2026-10-26",
              "check_out": "2026-10-27",
              "guests": 1
            }
          }
        ],
        "content": [
          {
            "date": "2026-10-26",
            "time": "14:00",
            "purpose": "客户会议",
            "description": "杭州客户会议，14:00开始",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "杭州",
        "destination": "南京",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "start_date": "2026-10-27",
          "departure_date": "2026-10-27",
          "check_in": "2026-10-27",
          "check_out": "2026-10-28",
          "nights": 1,
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "start_date": "user",
          "departure_date": "user",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "seat_class": "user",
          "train_kind": "user",
          "train_max_total_cny": "user"
        },
        "condition_strengths": {
          "seat_class": "hard",
          "train_kind": "hard",
          "train_max_total_cny": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "杭州",
              "destination": "南京",
              "departure_date": "2026-10-27",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "南京",
              "check_in": "2026-10-27",
              "check_out": "2026-10-28",
              "guests": 1
            }
          }
        ],
        "content": [
          {
            "date": "2026-10-27",
            "time": "14:00",
            "purpose": "项目交流",
            "description": "南京项目交流，14:00开始",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "南京",
        "destination": "上海",
        "purpose": "return",
        "requires_hotel": false,
        "conditions": {
          "start_date": "2026-10-28",
          "departure_date": "2026-10-28",
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "start_date": "user",
          "departure_date": "user",
          "seat_class": "user",
          "train_kind": "user",
          "train_max_total_cny": "user"
        },
        "condition_strengths": {
          "seat_class": "hard",
          "train_kind": "hard",
          "train_max_total_cny": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "南京",
              "destination": "上海",
              "departure_date": "2026-10-28",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "departure_at",
                "direction": "desc"
              }
            ]
          }
        ],
        "content": []
      }
    ]
  }
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "rejected",
  "operation": null,
  "trip_plan_id": null,
  "revision": null,
  "stop_reason": null,
  "stop_planning": null,
  "reason": "参数不符合当前工具结构。",
  "error": {
    "code": "INVALID_ARGUMENTS",
    "message": "参数不符合当前工具结构。",
    "details": {
      "fields": [
        {
          "field": "tripplan.tasks.*.purpose",
          "message": "此字段必须使用当前阶段允许的枚举：[\"visit\",\"return\",\"transit\",\"unspecified\"]",
          "type": "schema_validation"
        }
      ]
    }
  },
  "issues": null
}
```

### 第3次：agent:main

工具：query_policy；call_id：call_00_cMfPbeN3nti0PWnio3tY5061
```json
{
  "request_kind": "planning",
  "context": {
    "trip": "上海→杭州(10-26)→南京(10-27)→上海(10-28)，三段高铁二等座，每段交通硬预算500元，总参考预算2500元软偏好；杭州、南京各一晚；26日杭州14:00客户会议，27日南京14:00项目交流",
    "user_request": "明确差旅，需适用差旅政策"
  }
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "query_policy",
  "status": "partial",
  "operation": null,
  "trip_plan_id": null,
  "revision": null,
  "stop_reason": null,
  "stop_planning": null,
  "reason": null,
  "error": null,
  "issues": null
}
```

### 第4次：agent:travel_policy

政策工具内部的真实模型提取调用，不是一次新的Main决策。下列为政策工具最终返回：
```json
{
  "call_id": "call_00_cMfPbeN3nti0PWnio3tY5061",
  "name": "query_policy",
  "status": "partial",
  "data": {
    "status": "partial",
    "summary": "演示政策：适用演示差旅政策：三段高铁均须二等座且单程交通不超过500元；杭州、南京住宿各不超过400元/晚。总参考预算2500元为软偏好，不构成硬约束。房价与库存未知，保留未核验状态，不作预订付款。",
    "constraints": [
      {
        "id": "seat",
        "rule": "高铁二等座",
        "applicability": {},
        "hard": true,
        "evidence_ids": [
          "demo-policy"
        ],
        "field": "seat_class",
        "operator": "==",
        "value": "二等座",
        "unit": null
      },
      {
        "id": "train_max_total_cny",
        "rule": "单程交通演示额度500元",
        "applicability": {},
        "hard": true,
        "evidence_ids": [
          "demo-policy"
        ],
        "field": "train_max_total_cny",
        "operator": "<=",
        "value": 500,
        "unit": "CNY"
      },
      {
        "id": "hotel_hangzhou",
        "rule": "杭州住宿演示额度400元/晚",
        "applicability": {
          "city": "杭州"
        },
        "hard": true,
        "evidence_ids": [
          "demo-policy"
        ],
        "field": "hotel_max_nightly_cny",
        "operator": "<=",
        "value": 400,
        "unit": "CNY/night"
      },
      {
        "id": "hotel_nanjing",
        "rule": "南京住宿演示额度400元/晚",
        "applicability": {
          "city": "南京"
        },
        "hard": true,
        "evidence_ids": [
          "demo-policy"
        ],
        "field": "hotel_max_nightly_cny",
        "operator": "<=",
        "value": 400,
        "unit": "CNY/night"
      }
    ],
    "applicability": {},
    "references": [
      {
        "id": "demo-policy",
        "source": "demo-travel-policy.txt"
      }
    ],
    "effective_date": null,
    "missing_fields": [
      "policy_applicability:city",
      "demo_policy_not_authoritative"
    ],
    "conflicts": []
  }
}
```

### 第5次：agent:main

工具：write_trip；call_id：call_00_8TCI46EhGhd4cKGtWttM0310
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "propose",
    "default_round_trip": false,
    "reason": "三段差旅：上海→杭州→南京→上海，含两处会议与两晚住宿",
    "confirmed_conditions": {
      "passengers": 1,
      "constraints": {
        "seat_class": "二等座",
        "train_kind": "high_speed",
        "train_max_total_cny": 500,
        "total_budget_cny": 2500
      }
    },
    "field_sources": {
      "passengers": "proposal",
      "seat_class": "user",
      "train_kind": "user",
      "train_max_total_cny": "user",
      "total_budget_cny": "user"
    },
    "condition_strengths": {
      "seat_class": "hard",
      "train_kind": "hard",
      "train_max_total_cny": "hard",
      "total_budget_cny": "soft"
    },
    "tasks": [
      {
        "origin": "上海",
        "destination": "杭州",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-26",
          "arrival_before": "2026-10-26T13:00:00+08:00",
          "check_in": "2026-10-26",
          "check_out": "2026-10-27",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "hotel_max_nightly_cny": 400
          }
        },
        "field_sources": {
          "departure_date": "user",
          "arrival_before": "derived",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "passengers": "proposal",
          "guests": "proposal",
          "seat_class": "user",
          "train_kind": "user",
          "train_max_total_cny": "user",
          "hotel_max_nightly_cny": "context"
        },
        "condition_strengths": {
          "arrival_before": "soft",
          "hotel_max_nightly_cny": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "上海",
              "destination": "杭州",
              "departure_date": "2026-10-26",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "杭州",
              "check_in": "2026-10-26",
              "check_out": "2026-10-27",
              "guests": 1
            },
            "constraints": {
              "hotel_max_nightly_cny": 400
            },
            "ranking": [
              {
                "field": "nightly_cny",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": [
          {
            "purpose": "客户会议",
            "description": "14:00 杭州客户会议",
            "date": "2026-10-26",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "杭州",
        "destination": "南京",
        "purpose": "business",
        "requires_hotel": true,
        "conditions": {
          "departure_date": "2026-10-27",
          "arrival_before": "2026-10-27T13:00:00+08:00",
          "check_in": "2026-10-27",
          "check_out": "2026-10-28",
          "nights": 1,
          "passengers": 1,
          "guests": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500,
            "hotel_max_nightly_cny": 400
          }
        },
        "field_sources": {
          "departure_date": "user",
          "arrival_before": "derived",
          "check_in": "user",
          "check_out": "user",
          "nights": "user",
          "passengers": "proposal",
          "guests": "proposal",
          "seat_class": "user",
          "train_kind": "user",
          "train_max_total_cny": "user",
          "hotel_max_nightly_cny": "context"
        },
        "condition_strengths": {
          "arrival_before": "soft",
          "hotel_max_nightly_cny": "hard"
        },
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "杭州",
              "destination": "南京",
              "departure_date": "2026-10-27",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "arrival_at",
                "direction": "asc"
              }
            ]
          },
          {
            "domain": "hotel",
            "parameters": {
              "city": "南京",
              "check_in": "2026-10-27",
              "check_out": "2026-10-28",
              "guests": 1
            },
            "constraints": {
              "hotel_max_nightly_cny": 400
            },
            "ranking": [
              {
                "field": "nightly_cny",
                "direction": "asc"
              }
            ]
          }
        ],
        "content": [
          {
            "purpose": "项目交流",
            "description": "14:00 南京项目交流",
            "date": "2026-10-27",
            "time": "14:00",
            "source": "user",
            "strength": "hard"
          }
        ]
      },
      {
        "origin": "南京",
        "destination": "上海",
        "purpose": "return",
        "requires_hotel": false,
        "conditions": {
          "departure_date": "2026-10-28",
          "passengers": 1,
          "constraints": {
            "seat_class": "二等座",
            "train_kind": "high_speed",
            "train_max_total_cny": 500
          }
        },
        "field_sources": {
          "departure_date": "user",
          "passengers": "proposal",
          "seat_class": "user",
          "train_kind": "user",
          "train_max_total_cny": "user"
        },
        "condition_strengths": {},
        "items": [
          {
            "domain": "train",
            "parameters": {
              "origin": "南京",
              "destination": "上海",
              "departure_date": "2026-10-28",
              "passengers": 1
            },
            "constraints": {
              "seat_class": "二等座",
              "train_kind": "high_speed",
              "train_max_total_cny": 500
            },
            "ranking": [
              {
                "field": "departure_at",
                "direction": "asc"
              }
            ]
          }
        ]
      }
    ]
  }
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "partial",
  "operation": "propose",
  "trip_plan_id": "efebf3d6-64f9-4228-99db-7409026b8079",
  "revision": 1,
  "stop_reason": "awaiting_model",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "train_pending",
      "task_id": "t1",
      "field": "train",
      "message": "尚未选择有效车次。",
      "blocking": true
    },
    {
      "code": "hotel_pending",
      "task_id": "t1",
      "field": "hotel",
      "message": "尚未选择酒店地点。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "train_pending",
      "task_id": "t2",
      "field": "train",
      "message": "尚未选择有效车次。",
      "blocking": true
    },
    {
      "code": "hotel_pending",
      "task_id": "t2",
      "field": "hotel",
      "message": "尚未选择酒店地点。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "train_pending",
      "task_id": "t3",
      "field": "train",
      "message": "尚未选择有效车次。",
      "blocking": true
    },
    {
      "code": "policy_unverified",
      "field": "policy",
      "message": "差旅政策尚未核验。",
      "blocking": true
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "seat"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "seat"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t3",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t3"
      ],
      "evidence": "seat"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "train_max_total_cny"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "train_max_total_cny"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t3",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t3"
      ],
      "evidence": "train_max_total_cny"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "hotel_hangzhou"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "hotel_nanjing"
    }
  ]
}
```

### 第6次：agent:trip_review

工具：write_trip；call_id：call_00_DgQMH82mF9ANylGAuPX44688
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "select",
    "trip_plan_id": "efebf3d6-64f9-4228-99db-7409026b8079",
    "expected_revision": 2,
    "allow_repair": true,
    "reason": "选择三段高铁二等座及两处酒店地点",
    "tasks": [
      {
        "task_id": "t1",
        "selection_refs": {
          "train": {
            "query_id": "ff1661f9-972a-4829-b6cc-0f0055015a87",
            "result_revision": 1,
            "candidate_id": "demo-19502d92be43-0"
          },
          "hotel": {
            "query_id": "baf99a0e-a406-424d-a985-e558383f84f1",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t2",
        "selection_refs": {
          "train": {
            "query_id": "6d07b156-3a01-43e8-b882-765ba22bfa05",
            "result_revision": 1,
            "candidate_id": "demo-71b63fde423c-0"
          },
          "hotel": {
            "query_id": "6dba1ded-5362-40a1-98b9-7c53a6023b71",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t3",
        "selection_refs": {
          "train": {
            "query_id": "699910a4-bd56-4a0c-8493-d7a71011aee1",
            "result_revision": 1,
            "candidate_id": "demo-cae1e83795fa-0"
          }
        }
      }
    ]
  },
  "reply": {
    "explanation": "三段高铁均为二等座且单程不超演示额度，去程与两段城际均上午抵达，留出午后会议与交流时间；返程上午回沪。两处酒店仅确认地点，房价与库存未核验，未作预订付款。"
  },
  "round_summary": "选择三段高铁二等座与两处酒店地点，完成完整往返行程。"
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "partial",
  "operation": "select",
  "trip_plan_id": "efebf3d6-64f9-4228-99db-7409026b8079",
  "revision": 3,
  "stop_reason": "awaiting_model",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "policy_unverified",
      "field": "policy",
      "message": "差旅政策尚未核验。",
      "blocking": true
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "hotel_hangzhou"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "hotel_nanjing"
    }
  ]
}
```

### 第7次：agent:trip_review

工具：write_trip；call_id：call_00_Dn717Hb8Sqhu0K28aArZ6998
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "select",
    "trip_plan_id": "efebf3d6-64f9-4228-99db-7409026b8079",
    "expected_revision": 3,
    "allow_repair": true,
    "reason": "三段高铁与两处酒店地点均已按用户条件选定，保留现有选择。",
    "tasks": [
      {
        "task_id": "t1",
        "selection_refs": {
          "train": {
            "query_id": "ff1661f9-972a-4829-b6cc-0f0055015a87",
            "result_revision": 1,
            "candidate_id": "demo-19502d92be43-0"
          },
          "hotel": {
            "query_id": "baf99a0e-a406-424d-a985-e558383f84f1",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t2",
        "selection_refs": {
          "train": {
            "query_id": "6d07b156-3a01-43e8-b882-765ba22bfa05",
            "result_revision": 1,
            "candidate_id": "demo-71b63fde423c-0"
          },
          "hotel": {
            "query_id": "6dba1ded-5362-40a1-98b9-7c53a6023b71",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t3",
        "selection_refs": {
          "train": {
            "query_id": "699910a4-bd56-4a0c-8493-d7a71011aee1",
            "result_revision": 1,
            "candidate_id": "demo-cae1e83795fa-0"
          }
        }
      }
    ]
  },
  "reply": {},
  "round_summary": "核对完整往返三段车次与两晚住宿覆盖，确认选择并保存。"
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "partial",
  "operation": "select",
  "trip_plan_id": "efebf3d6-64f9-4228-99db-7409026b8079",
  "revision": 4,
  "stop_reason": "awaiting_model",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "policy_unverified",
      "field": "policy",
      "message": "差旅政策尚未核验。",
      "blocking": true
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "hotel_hangzhou"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "hotel_nanjing"
    }
  ]
}
```

### 第8次：agent:trip_review

工具：write_trip；call_id：call_00_GQDbpzood1D2PGrQLFy20122
```json
{
  "request_kind": "planning",
  "tripplan": {
    "operation": "select",
    "trip_plan_id": "efebf3d6-64f9-4228-99db-7409026b8079",
    "expected_revision": 4,
    "allow_repair": true,
    "reason": "确认三段高铁二等座与两处酒店地点，保留房价库存未核验状态。",
    "tasks": [
      {
        "task_id": "t1",
        "selection_refs": {
          "train": {
            "query_id": "ff1661f9-972a-4829-b6cc-0f0055015a87",
            "result_revision": 1,
            "candidate_id": "demo-19502d92be43-0"
          },
          "hotel": {
            "query_id": "baf99a0e-a406-424d-a985-e558383f84f1",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t2",
        "selection_refs": {
          "train": {
            "query_id": "6d07b156-3a01-43e8-b882-765ba22bfa05",
            "result_revision": 1,
            "candidate_id": "demo-71b63fde423c-0"
          },
          "hotel": {
            "query_id": "6dba1ded-5362-40a1-98b9-7c53a6023b71",
            "result_revision": 1,
            "candidate_id": "demo-hotel-1"
          }
        }
      },
      {
        "task_id": "t3",
        "selection_refs": {
          "train": {
            "query_id": "699910a4-bd56-4a0c-8493-d7a71011aee1",
            "result_revision": 1,
            "candidate_id": "demo-cae1e83795fa-0"
          }
        }
      }
    ]
  },
  "reply": {},
  "round_summary": "确认完整往返三段车次与两晚住宿覆盖，保存选择；房价库存与政策证据仍为未核验状态。"
}
```

实际结果（保留状态、错误和校验问题；完整快照见原始JSON）：
```json
{
  "name": "write_trip",
  "status": "partial",
  "operation": "select",
  "trip_plan_id": "efebf3d6-64f9-4228-99db-7409026b8079",
  "revision": 5,
  "stop_reason": "awaiting_model",
  "stop_planning": false,
  "reason": "",
  "error": null,
  "issues": [
    {
      "code": "hotel_price_unknown",
      "task_id": "t1",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "hotel_price_unknown",
      "task_id": "t2",
      "field": "hotel",
      "message": "酒店地点信息不包含房价和库存。",
      "blocking": true
    },
    {
      "code": "policy_unverified",
      "field": "policy",
      "message": "差旅政策尚未核验。",
      "blocking": true
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t1",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t1"
      ],
      "evidence": "hotel_hangzhou"
    },
    {
      "code": "policy_constraint_unverified",
      "task_id": "t2",
      "message": "缺少验证企业强制约束的有效证据。",
      "blocking": true,
      "task_ids": [
        "t2"
      ],
      "evidence": "hotel_nanjing"
    }
  ]
}
```

### 最终输出

```text
演示数据，仅用于展示，不代表真实车次、报价或房态。

已保留以下安排，仍有待处理问题：

上海→杭州：
演示 G1001，2026-10-26 09:00出发，2026-10-26 11:00抵达，二等座，100元。
住宿：杭州演示酒店地点，2026-10-26入住、2026-10-27退房；房价与房态未核实。
活动：2026-10-26 14:00 客户会议；14:00 杭州客户会议。

杭州→南京：
演示 G1001，2026-10-27 09:00出发，2026-10-27 11:00抵达，二等座，100元。
住宿：南京演示酒店地点，2026-10-27入住、2026-10-28退房；房价与房态未核实。
活动：2026-10-27 14:00 项目交流；14:00 南京项目交流。

南京→上海：
演示 G1001，2026-10-28 09:00出发，2026-10-28 11:00抵达，二等座，100元。

交通小计：300元（不含住宿）。

企业政策仍有未核实事项，不能确认合规完成。

待处理：酒店地点信息不包含房价和库存。
```

## 核对位置

- 原始证据：.cache/reports/travel-source-model-2026-10-24-round6.json（完整工具参数、输出、模型Usage和落库计划）。
- Go静态提示：backend/internal/agent/agents/travel/prompts/；SHA基线：openspec/changes/archive/2026-10-25-preserve-travel-source-flow/source-baseline.json。
- Go部分结果/停止判定：backend/internal/service/trip/patches.go finishSourcePlan；原版：trip_assistant/trips/service.py _capability_gap_only。
- 原验收判据：backend/tests/integration/travel_source_model_test.go，仅检查多城1份计划/3段/3车/2酒店/至少2活动，没有检查business语义与重复select。
