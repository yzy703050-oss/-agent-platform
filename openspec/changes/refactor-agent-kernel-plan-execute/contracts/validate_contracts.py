"""Validate design fixtures, not production state or provider behavior.

Requires jsonschema >= 4.18. Run from any directory; no network refs are used.
Only checks graph/source relationships present in these fixtures. Authorization,
transactions, runtime recovery and semantic fidelity require implementation tests.
"""

from __future__ import annotations

import copy
import hashlib
import json
import re
from datetime import date, datetime
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker

ROOT = Path(__file__).resolve().parent
SCHEMA = json.loads((ROOT / "agent-contracts.schema.json").read_text(encoding="utf-8"))
FORMATS = FormatChecker()


@FORMATS.checks("date", raises=ValueError)
def valid_date(value):
    if not isinstance(value, str):
        return True
    return bool(re.fullmatch(r"\d{4}-\d{2}-\d{2}", value)) and bool(date.fromisoformat(value))


@FORMATS.checks("date-time", raises=ValueError)
def valid_timestamp(value):
    if not isinstance(value, str):
        return True
    if not re.fullmatch(r"\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[Zz]|[+-]\d{2}:\d{2})", value):
        return False
    return datetime.fromisoformat(value.upper()).utcoffset() is not None


def require(condition, message):
    if not condition:
        raise ValueError(message)


def index_by(values, key):
    result = {v[key]: v for v in values}
    require(len(result) == len(values), f"duplicate {key}")
    return result


def check_plan(plan):
    stops = index_by(plan["stops"], "stop_id")
    legs = index_by(plan["legs"], "leg_id")
    tasks = index_by(plan["tasks"], "task_id")
    requirements = index_by(plan["requirements"], "requirement_id")
    items = index_by([i for t in tasks.values() for i in t["items"]], "item_id")
    for leg in legs.values():
        require(leg["from_stop_id"] in stops and leg["to_stop_id"] in stops, "dangling leg stop")
        require(leg["from_stop_id"] != leg["to_stop_id"], "leg endpoints must use distinct stop IDs")
    for stop in stops.values():
        if stop.get("arrival_date") and stop.get("departure_date"):
            require(stop["arrival_date"] <= stop["departure_date"], "reversed stop dates")
    for requirement in requirements.values():
        affected = requirement["task_ids"]
        require(len(set(affected)) == len(affected), "duplicate requirement task")
        require(set(affected) <= tasks.keys(), "dangling requirement task")
        require(bool(affected) == (requirement["scope"] == "tasks"), "requirement scope mismatch")
        source_kind = {"user": "message", "policy": "policy", "memory": "memory"}[requirement["origin"]]
        require(any(s["kind"] == source_kind for s in requirement["sources"]), "origin/source kind mismatch")
    for task in tasks.values():
        target = task["target"]
        if target["kind"] != "plan":
            require(target["id"] in (stops if target["kind"] == "stop" else legs), "dangling task target")
        refs = task["requirement_ids"]
        require(len(refs) == len(set(refs)), "duplicate task requirement")
        require(set(refs) <= requirements.keys(), "dangling task requirement")
        expected = {r["requirement_id"] for r in requirements.values() if task["task_id"] in r["task_ids"]}
        require(expected == set(refs), "task-scoped requirement references disagree")
        for item in task["items"]:
            require(item["domain"] in task["parameters"], "missing task domain parameters object")
        hotel = task["parameters"].get("hotel", {})
        if hotel.get("check_in") and hotel.get("check_out"):
            require(hotel["check_in"] < hotel["check_out"], "hotel checkout must follow checkin")
    visited, active = set(), set()

    def visit(item_id):
        require(item_id in items, "dangling item dependency")
        require(item_id not in active, "cyclic query dependency")
        if item_id in visited:
            return
        active.add(item_id)
        dependencies = items[item_id]["depends_on"]
        require(len(dependencies) == len(set(dependencies)), "duplicate dependency")
        for dependency in dependencies:
            visit(dependency)
        active.remove(item_id)
        visited.add(item_id)

    for item_id in items:
        visit(item_id)


def check_snapshot(snapshot):
    check_plan(snapshot["definition"])
    tasks = index_by(snapshot["definition"]["tasks"], "task_id")
    versions = index_by(snapshot["task_versions"], "task_id")
    require(tasks.keys() == versions.keys(), "task version coverage mismatch")
    index_by(snapshot["selections"], "selection_id")
    for selection in snapshot["selections"]:
        task_id = selection["task_id"]
        require(task_id in tasks, "dangling selection task")
        require(selection["task_revision"] == versions[task_id]["task_revision"], "stale selection task version")
        require(selection["item_id"] in {i["item_id"] for i in tasks[task_id]["items"]}, "dangling selection item")


UPDATE_FIELDS = {
    "update_plan": {"title", "goal", "purpose", "timezone"},
    "update_stop": {"city", "arrival_date", "departure_date"},
    "update_leg": {"from_stop_id", "to_stop_id", "departure_date"},
    "update_task": {"title", "target", "parameters", "requirement_ids", "ranking", "items"},
    "update_requirement": {"text", "origin", "scope", "task_ids", "sources"},
}
PARAMETER_FIELDS = {
    "hotel": {"city", "check_in", "check_out", "guests", "keywords"},
    "train": {"origin", "destination", "departure_date", "passengers", "keywords"},
    "web": {"query", "time_range"},
    "weather": {"city", "date"},
}


def check_patch(patch):
    for operation in patch["operations"]:
        if operation["op"] not in UPDATE_FIELDS:
            if operation["op"].startswith("reorder_") or operation["op"] == "refresh_queries":
                key = {"reorder_stops": "stop_ids", "reorder_tasks": "task_ids", "refresh_queries": "item_ids"}[operation["op"]]
                require(len(operation[key]) == len(set(operation[key])), "duplicate operation IDs")
            continue
        changes = operation["changes"]
        paths = list(changes.get("set", {})) + changes.get("unset", [])
        require(paths, "empty changes")
        require(len(paths) == len(set(paths)), "set/unset overlap")
        for path in paths:
            parts = path.split(".")
            require(parts[0] in UPDATE_FIELDS[operation["op"]], "protected or unknown patch path")
            if len(parts) > 1:
                require(operation["op"] == "update_task" and parts[0] == "parameters", "unsupported nested path")
                require(len(parts) <= 3 and parts[1] in PARAMETER_FIELDS, "unknown parameter path")
                if len(parts) == 3:
                    require(parts[2] in PARAMETER_FIELDS[parts[1]], "unknown parameter field")
        for a in paths:
            for b in paths:
                require(a == b or not b.startswith(a + "."), "ancestor/descendant patch overlap")


def check_view(view):
    check_snapshot(view["snapshot"])
    versions = {v["task_id"]: v["task_revision"] for v in view["snapshot"]["task_versions"]}
    expected = {(t["task_id"], i["item_id"]) for t in view["snapshot"]["definition"]["tasks"] for i in t["items"]}
    actual = [(i["task_id"], i["item_id"]) for i in view["items"]]
    require(len(actual) == len(set(actual)) and set(actual) == expected, "runtime item coverage mismatch")
    for item in view["items"]:
        require(item["task_revision"] == versions[item["task_id"]], "runtime task version mismatch")
        if item["status"] == "succeeded":
            require(item["last_successful_task_revision"] == item["task_revision"], "old success presented as current")
        if item["status"] == "pending":
            require(item["needs_query"], "pending query suppressed")


def semantic_check(kind, data):
    if kind == "PlanDefinition":
        check_plan(data)
    elif kind == "PlanSnapshot":
        check_snapshot(data)
    elif kind == "PlanView":
        check_view(data)
    elif kind == "PlanPatch":
        check_patch(data)
    elif kind == "ReplannerDecision" and data["kind"] == "patch":
        check_patch(data["patch"])
    elif kind == "ReplannerInput":
        check_view(data["plan"])
        require(data["plan"]["snapshot"]["plan_id"] == data["execution"]["plan_id"], "report plan mismatch")
        require(data["plan"]["snapshot"]["revision"] == data["execution"]["plan_revision"], "report revision mismatch")
    elif kind == "ExecutionRequest":
        hotel = data["input"] if data["domain"] == "hotel" else {}
        if hotel:
            require(hotel["check_in"] < hotel["check_out"], "execution hotel dates reversed")
    elif kind == "ExecutionResult":
        index_by(data["candidates"], "candidate_id")
        require((data["evidence_id"] is None) == (data["result_revision"] is None), "partial evidence identity")
        if data["status"] in {"failed", "blocked", "cancelled"}:
            require(not data["candidates"], "failure presents successful candidates")
    elif kind == "MemoryAgentInput":
        require(data["from_round"] <= data["through_round"], "reversed memory coverage")
        index_by(data["messages"], "message_id")
    elif kind == "MemoryMaintenanceResult":
        require(data["summarized_through_round"] <= data["reviewed_through_round"], "summary beyond review coverage")
        require((data["status"] == "succeeded") == (data["error"] is None), "maintenance error/status mismatch")
    elif kind == "GeneralContext":
        review = data["memory_review"]
        require(review["summarized_through_round"] <= review["reviewed_through_round"], "general summary beyond review coverage")
    elif kind == "UserMemorySnapshot":
        index_by(data["memories"], "memory_id")


def validate(kind, data):
    validator = Draft202012Validator({**SCHEMA, "$ref": f"#/$defs/{kind}"}, format_checker=FORMATS)
    errors = sorted(validator.iter_errors(data), key=lambda e: str(list(e.path)))
    require(not errors, "; ".join(f"{list(e.path)}: {e.message}" for e in errors[:3]))
    semantic_check(kind, data)


def main():
    Draft202012Validator.check_schema(SCHEMA)
    manifest = json.loads((ROOT / "examples/manifest.json").read_text(encoding="utf-8"))
    fixtures = {}
    for example in manifest["examples"]:
        filename, kind = example["file"], example["definition"]
        require(filename not in fixtures and Path(filename).name == filename, "invalid manifest entry")
        data = json.loads((ROOT / "examples" / filename).read_text(encoding="utf-8"))
        validate(kind, data)
        fixtures[filename] = (kind, data)
    actual_files = {p.name for p in (ROOT / "examples").glob("*.json") if p.name != "manifest.json"}
    require(actual_files == fixtures.keys(), "unlisted example fixture")
    multicity_path = ROOT.parents[3] / "docs/fixtures/tripplan-multicity-snapshot.example.json"
    require(json.loads(multicity_path.read_text(encoding="utf-8")) == fixtures["26-multicity-plan.json"][1], "multicity copies diverged")

    # Check relationships across the branching narrative without pretending to apply Patch.
    v1 = fixtures["05-plan-v1.json"][1]
    v2 = fixtures["10-plan-v2.json"][1]
    require(v2["revision"] == v1["revision"] + 1, "plan revision sequence")
    require(v2["task_versions"][0]["task_revision"] == v1["task_versions"][0]["task_revision"] + 1, "changed task not versioned")
    require(all(i["needs_query"] for i in fixtures["11-plan-v2-runtime-view.json"][1]["items"]), "changed task does not requery all Items")
    selected = fixtures["28-selection-only-plan-v2.json"][1]
    require(selected["task_versions"] == v1["task_versions"], "selection causes query version change")
    evidence = fixtures["07-execution-result.json"][1]
    selection = selected["selections"][0]
    require(selection["evidence"]["evidence_id"] == evidence["evidence_id"], "selection evidence not in fixture")
    require(selection["evidence"]["result_revision"] == evidence["result_revision"], "selection evidence revision mismatch")
    require(selection["evidence"]["candidate_id"] in {c["candidate_id"] for c in evidence["candidates"]}, "fabricated selected candidate")
    memory_input = fixtures["21-memory-agent-input.json"][1]
    user_ids = {m["message_id"] for m in memory_input["messages"] if m["role"] == "user"}
    for update in fixtures["22-memory-proposal.json"][1]["updates"]:
        require(any(s["kind"] == "message" and s["id"] in user_ids for s in update["sources"]), "memory lacks user source in fixture")
    require(fixtures["25-memory-no-change-result.json"][1]["memory_revision"] == memory_input["base_memory_revision"], "empty update creates memory revision")

    direct = fixtures["34-general-direct-query.json"][1]
    shared = fixtures["35-shared-query-evidence.json"][1]
    travel_direct = fixtures["36-travel-entry-direct-query.json"][1]
    require(shared["tool_call_record_id"] == direct["id"], "shared evidence call mismatch")
    require(shared["conversation_id"] == direct["conversation_id"], "shared evidence scope mismatch")
    require(direct["result_refs"][0] == {"evidence_id": shared["id"], "result_revision": shared["result_revision"]}, "shared evidence reference mismatch")
    require(travel_direct["agent_kind"] == "travel" and travel_direct["thread_role"] == "entry", "travel direct query uses plan thread")

    submit = fixtures["38-travel-submit.json"][1]
    trusted = fixtures["39-trusted-travel-meta.json"][1]
    receipt = fixtures["40-travel-task-receipt.json"][1]
    # These fixtures contain only strings and small integers with ASCII keys;
    # compact sorted JSON matches JCS here, not a general JCS implementation.
    hash_bytes = json.dumps({"args": submit["args"], "scope": trusted, "schema_version": 1},
                            ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
    require(hashlib.sha256(hash_bytes).hexdigest() == submit["input_hash"], "runtime fixture input hash mismatch")
    for filename in ("40-travel-task-receipt.json", "41-travel-task-result.json", "42-travel-task-failed.json"):
        value = fixtures[filename][1]
        require(value["request_key"] == submit["request_key"] and value["input_hash"] == submit["input_hash"], "runtime identity mismatch")
        require(value["task_id"] == receipt["task_id"], "runtime task identity mismatch")
    require(receipt["deadline_at"] == trusted["deadline_at"], "runtime deadline changed")
    validate("TravelTaskFind", {"schema_version": 1, "request_key": submit["request_key"], "input_hash": submit["input_hash"]})
    require(fixtures["37-runtime-dispatch.json"][1]["delegation_id"] == trusted["delegation_id"], "dispatch delegation mismatch")

    negatives = []

    def reject(name, filename, change):
        kind, data = fixtures[filename]
        data = copy.deepcopy(data)
        change(data)
        try:
            validate(kind, data)
        except ValueError:
            negatives.append(name)
            return
        raise ValueError(f"negative fixture unexpectedly accepted: {name}")

    reject("Item carries query parameters", "04-planner-draft.json", lambda d: d["tasks"][0]["items"][0].update(parameters={}))
    reject("Item carries needs_query", "04-planner-draft.json", lambda d: d["tasks"][0]["items"][0].update(needs_query=False))
    reject("select Item", "04-planner-draft.json", lambda d: d["tasks"][0]["items"][0].update(domain="select"))
    reject("hard/soft requirement", "04-planner-draft.json", lambda d: d["requirements"][0].update(strength="hard"))
    reject("model injects user identity", "01-travel-tool-create.json", lambda d: d.update(user_id="other_user"))
    reject("continue missing target", "14-travel-tool-continue.json", lambda d: d.pop("target"))
    reject("missing execution input", "06-execution-request.json", lambda d: d["input"].pop("check_in"))
    reject("unknown execution guests", "06-execution-request.json", lambda d: d["input"].update(guests=None))
    reject("invalid date", "06-execution-request.json", lambda d: d["input"].update(check_in="2026-02-30"))
    reject("reversed hotel dates", "06-execution-request.json", lambda d: d["input"].update(check_out="2026-11-01"))
    reject("fractional minor currency", "07-execution-result.json", lambda d: d["candidates"][0].update(price={"amount_minor":1.2,"currency":"CNY","basis":"night"}))
    reject("empty result has candidates", "07-execution-result.json", lambda d: d.update(coverage="empty"))
    reject("success has no evidence", "07-execution-result.json", lambda d: d.update(evidence_id=None))
    reject("failure lacks reason", "17-retry-exhausted-result.json", lambda d: d.update(error=None))
    reject("dangling task target", "04-planner-draft.json", lambda d: d["tasks"][0].update(target={"kind":"stop","id":"missing"}))
    reject("duplicate Task ID", "04-planner-draft.json", lambda d: d["tasks"].append(copy.deepcopy(d["tasks"][0])))
    reject("dangling requirement", "04-planner-draft.json", lambda d: d["tasks"][0]["requirement_ids"].append("missing"))
    reject("source origin escalation", "04-planner-draft.json", lambda d: d["requirements"][0].update(origin="policy"))
    reject("query dependency cycle", "04-planner-draft.json", lambda d: d["tasks"][0]["items"][0].update(depends_on=["item_hotel"]))
    reject("missing task version", "05-plan-v1.json", lambda d: d.update(task_versions=[]))
    reject("stale selection", "28-selection-only-plan-v2.json", lambda d: d["selections"][0].update(task_revision=2))
    reject("old success marked current", "08-replanner-input.json", lambda d: d["plan"]["items"][0].update(last_successful_task_revision=2))
    reject("protected Patch path", "09-replanner-patch.json", lambda d: d["patch"]["operations"][0]["changes"].update(set={"task_id":"injected"}))
    reject("Patch ancestor overlap", "09-replanner-patch.json", lambda d: d["patch"]["operations"][0]["changes"].update(set={"parameters":{},"parameters.web":{}}))
    reject("Patch set/unset overlap", "09-replanner-patch.json", lambda d: d["patch"]["operations"][0]["changes"].update(unset=["items"]))
    reject("Patch empty changes", "09-replanner-patch.json", lambda d: d["patch"]["operations"][0].update(changes={}))
    reject("accepted is not saved", "20-memory-accepted.json", lambda d: d.update(saved=True))
    reject("summary exceeds review", "25-memory-no-change-result.json", lambda d: d.update(summarized_through_round=11))
    reject("memory self-promotes authority", "22-memory-proposal.json", lambda d: d["updates"][0].update(authority="explicit"))
    reject("explicit write self-promotes authority", "31-explicit-memory-write.json", lambda d: d["update"].update(authority="explicit"))
    reject("general context invents execution state", "30-general-context.json", lambda d: d.update(checkpoint_status="finished"))
    reject("duplicate memory in user snapshot", "33-user-memory-snapshot.json", lambda d: d["memories"].append(copy.deepcopy(d["memories"][0])))
    reject("direct query forced plan", "34-general-direct-query.json", lambda d: d.update(plan_id="fabricated_plan"))
    reject("direct query forced delegation", "36-travel-entry-direct-query.json", lambda d: d.update(delegation_id="fabricated_delegation"))
    reject("successful call missing evidence", "34-general-direct-query.json", lambda d: d.update(result_refs=[]))
    reject("unknown call mode", "34-general-direct-query.json", lambda d: d.update(mode="silent_fallback"))
    reject("shared evidence forced task", "35-shared-query-evidence.json", lambda d: d.update(task_id="fabricated_task"))
    reject("zero dispatch generation", "37-runtime-dispatch.json", lambda d: d.update(dispatch_seq=0))
    reject("credential in dispatch", "37-runtime-dispatch.json", lambda d: d.update(api_key="fixture-not-a-secret"))
    reject("malformed input hash", "38-travel-submit.json", lambda d: d.update(input_hash="not-a-hash"))
    reject("model arguments inject runtime identity", "38-travel-submit.json", lambda d: d["args"].update(subject_id="other"))
    reject("missing trusted scope", "39-trusted-travel-meta.json", lambda d: d.pop("scope_id"))
    reject("request identity missing", "40-travel-task-receipt.json", lambda d: d.pop("request_key"))
    reject("accepted receipt is not a final result", "41-travel-task-result.json", lambda d: d.update(status="queued"))
    reject("failed task cannot claim successful call", "41-travel-task-result.json", lambda d: d.update(status="failed"))
    reject("successful task cannot claim failed call", "42-travel-task-failed.json", lambda d: d.update(status="succeeded"))
    reject("missing final result", "41-travel-task-result.json", lambda d: d.pop("result"))
    print(f"PASS: schema meta-validation; {len(fixtures)} examples; multicity fixture parity; cross-example references; {len(negatives)} negative cases")
    print("LIMIT: no database, authorization, Eino execution, semantic summary verification or supplier integration was exercised")


if __name__ == "__main__":
    main()
