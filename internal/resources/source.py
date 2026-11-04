"""Read SDK references with Python's AST; never import or execute project code."""
import ast
import json
import sys

KINDS = {"Table", "TransformOutput", "DatabaseTable", "Database", "ApiRunner", "Secret", "Input"}


def references(files, kind):
    trees = {path: ast.parse(text, filename=path) for path, text in files.items()}
    modules = {path.removesuffix(".py").replace("/", ".").removesuffix(".__init__"): path for path in trees}
    aliases, bindings = {}, {}
    for path, tree in trees.items():
        aliases[path], bindings[path] = {}, {}
        module = path.removesuffix(".py").replace("/", ".")
        package = module.removesuffix(".__init__") if path.endswith("__init__.py") else module.rpartition(".")[0]
        for node in tree.body:
            if isinstance(node, ast.Import):
                for entry in node.names:
                    aliases[path][entry.asname or entry.name.split(".")[0]] = (entry.name if entry.asname else entry.name.split(".")[0])
            elif isinstance(node, ast.ImportFrom):
                prefix = node.module or ""
                if node.level:
                    parent = package.split(".") if package else []
                    prefix = ".".join(parent[:len(parent)-node.level+1] + ([prefix] if prefix else []))
                for entry in node.names:
                    aliases[path][entry.asname or entry.name] = prefix + "." + entry.name
            elif isinstance(node, (ast.Assign, ast.AnnAssign)):
                targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                for target in targets:
                    if isinstance(target, ast.Name) and node.value is not None:
                        bindings[path][target.id] = node.value

    def origin(path, node):
        if isinstance(node, ast.Name): return aliases[path].get(node.id, node.id)
        if isinstance(node, ast.Attribute): return origin(path, node.value) + "." + node.attr
        return ""

    def sdk(path, node, seen=None):
        name = origin(path, node)
        seen = set() if seen is None else seen
        identity = (path, name)
        if identity in seen:
            return ""
        seen.add(identity)
        if name.startswith("vortexify."):
            return name.rsplit(".", 1)[-1]
        module, _, member = name.rpartition(".")
        target = modules.get(module)
        if target:
            return sdk(target, ast.Name(id=member), seen)
        if isinstance(node, ast.Name):
            bound = bindings[path].get(node.id)
            if isinstance(bound, (ast.Name, ast.Attribute)):
                return sdk(path, bound, seen)
        return ""

    visiting = set()
    def value(path, node):
        identity = (path, ast.dump(node))
        if identity in visiting: raise ValueError("Circular SDK constant")
        visiting.add(identity)
        try:
            if isinstance(node, (ast.Name, ast.Attribute)):
                if isinstance(node, ast.Name) and node.id in bindings[path]: return value(path, bindings[path][node.id])
                module, _, name = origin(path, node).rpartition(".")
                target = modules.get(module)
                if target and name in bindings[target]: return value(target, bindings[target][name])
            if isinstance(node, ast.Dict):
                if any(key is None for key in node.keys): raise ValueError("Expanded SDK declarations cannot be resolved locally")
                return {value(path, key): value(path, item) for key, item in zip(node.keys, node.values)}
            if isinstance(node, (ast.List, ast.Tuple)): return [value(path, item) for item in node.elts]
            if isinstance(node, ast.Call):
                name = sdk(path, node.func)
                if name == "CronSchedule": return None
                if name == "TriggerSchedule":
                    triggers = next((kw.value for kw in node.keywords if kw.arg == "triggers"), None)
                    if triggers is None: raise ValueError("TriggerSchedule requires triggers")
                    return {"triggers": value(path, triggers)}
                if name not in KINDS: raise ValueError("Dynamic SDK reference cannot be resolved locally")
                if name == "Input": return None
                if any(kw.arg is None for kw in node.keywords): raise ValueError("Expanded SDK references cannot be resolved locally")
                fields = {kw.arg: value(path, kw.value) for kw in node.keywords}
                first = {"Table": "alias", "Secret": "alias", "TransformOutput": "transform", "DatabaseTable": "reference", "Database": "name", "ApiRunner": "api"}[name]
                if node.args:
                    if len(node.args) != 1 or first in fields: raise ValueError("Invalid SDK reference arguments")
                    fields[first] = value(path, node.args[0])
                text = fields.get(first)
                if not isinstance(text, str) or not text.strip(): raise ValueError("SDK reference must use a static nonempty name")
                parts = text.split(".")
                if any(not p or p.strip() != p or any(c.isspace() for c in p) for p in parts): raise ValueError("Invalid dotted SDK reference")
                if name in {"Table", "DatabaseTable"} and len(parts) != 2: raise ValueError(name + " requires two dotted names")
                if name == "TransformOutput" and len(parts) not in (1, 2): raise ValueError("TransformOutput requires transform or transform.output")
                return {"kind": name, "name": text, "api_id": fields.get("api_id", "")}
            return ast.literal_eval(node)
        except (TypeError, KeyError) as error:
            raise ValueError("SDK declaration cannot be resolved locally") from error
        finally:
            visiting.remove(identity)

    result, found = [], 0
    def collect(items):
        if isinstance(items, dict):
            if items.get("kind") in KINDS: result.append(items)
            else:
                for item in items.values(): collect(item)
        elif isinstance(items, list):
            for item in items: collect(item)

    for path, tree in trees.items():
        for node in tree.body:
            if kind in {"api", "transform"} and isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                for decorator in node.decorator_list:
                    if isinstance(decorator, ast.Call) and sdk(path, decorator.func) in {"vxfy_api", "vxfy_transform"}:
                        declared = "api" if sdk(path, decorator.func) == "vxfy_api" else "transform"
                        if declared != kind: raise ValueError("SDK entrypoint type differs from resource folder")
                        found += 1
                        for keyword in decorator.keywords:
                            if keyword.arg == "inputs": collect(value(path, keyword.value))
                            # Trigger references also require existing upstream resources.
                            elif keyword.arg == "schedule": collect(value(path, keyword.value))
            elif kind == "agent" and isinstance(node, (ast.Assign, ast.AnnAssign)):
                call = node.value
                if isinstance(call, ast.Call) and sdk(path, call.func) == "Agent":
                    found += 1
                    for keyword in call.keywords:
                        if keyword.arg == "name":
                            raise ValueError("Agent.name was removed; identity comes from the registered resource")
                        if keyword.arg in {"context", "tools"}: collect(value(path, keyword.value))
            elif kind in {"connector", "sync"} and isinstance(node, ast.ClassDef):
                if any(sdk(path, base) == ("Connector" if kind == "connector" else "Sync") for base in node.bases): found += 1
    if found != 1: raise ValueError("Expected exactly one " + kind + " SDK entrypoint")
    return result


if __name__ == "__main__":
    try:
        request = json.load(sys.stdin)
        print(json.dumps({"references": references(request["files"], request["type"])}))
    except (ValueError, SyntaxError) as error:
        print(json.dumps({"error": str(error)}))
        sys.exit(1)
