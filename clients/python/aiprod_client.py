#!/usr/bin/env python3
"""Thin Python client for the aiprod REST API."""

import json
import requests


class AiprodClient:
    def __init__(self, base_url, api_key=None):
        self.base_url = base_url.rstrip("/")
        self.headers = {}
        if api_key:
            self.headers["Authorization"] = f"Bearer {api_key}"
        self.headers["Content-Type"] = "application/json"

    def _url(self, path):
        return f"{self.base_url}/api/v1{path}"

    def _get(self, path, params=None):
        r = requests.get(self._url(path), headers=self.headers, params=params, timeout=30)
        r.raise_for_status()
        return r.json().get("data")

    def _post(self, path, data=None):
        r = requests.post(self._url(path), headers=self.headers, json=data or {}, timeout=30)
        r.raise_for_status()
        return r.json().get("data")

    def _patch(self, path, data=None):
        r = requests.patch(self._url(path), headers=self.headers, json=data or {}, timeout=30)
        r.raise_for_status()
        return r.json().get("data")

    def _delete(self, path):
        r = requests.delete(self._url(path), headers=self.headers, timeout=30)
        r.raise_for_status()
        return r.json().get("data")

    # --- Memory ---
    def store_memory(self, namespace, key, value, metadata=None):
        return self._post("/memory", {
            "namespace": namespace, "key": key, "content": value,
            "metadata": metadata or {},
        })

    def recall_memory(self, namespace, key=None):
        params = {"namespace": namespace}
        if key:
            params["q"] = key
        return self._get("/memory", params=params)

    def list_memories(self, namespace):
        return self._get("/memory", params={"namespace": namespace})

    def scratchpad_write(self, namespace, content):
        return self._post("/scratchpad", {"key": namespace, "value": content})

    def scratchpad_read(self, namespace):
        return self._get("/scratchpad")

    # --- Docs ---
    def create_doc(self, title, body, tags=None):
        return self._post("/docs", {"title": title, "content": body, "tags": tags or []})

    def get_doc(self, doc_id):
        return self._get(f"/docs/{doc_id}")

    def list_docs(self, tag=None):
        params = {"tag": tag} if tag else None
        return self._get("/docs", params=params)

    def update_doc(self, doc_id, body=None, title=None):
        data = {}
        if body is not None: data["content"] = body
        if title is not None: data["title"] = title
        return self._patch(f"/docs/{doc_id}", data)

    # --- Tasks ---
    def create_task(self, title, description="", status="open"):
        return self._post("/tasks", {
            "title": title, "description": description, "status": status,
        })

    def get_task(self, task_id):
        return self._get(f"/tasks/{task_id}")

    def list_tasks(self, status=None):
        params = {"status": status} if status else None
        return self._get("/tasks", params=params)

    def update_task(self, task_id, status=None, title=None):
        data = {}
        if status: data["status"] = status
        if title: data["title"] = title
        return self._patch(f"/tasks/{task_id}", data)

    # --- Knowledge ---
    def add_fact(self, subject, predicate, object_val, confidence=1.0):
        return self._post("/facts", {
            "subject": subject, "predicate": predicate,
            "object": object_val, "confidence": confidence,
        })

    def query_facts(self, subject=None, predicate=None, object_val=None):
        params = {}
        if subject: params["subject"] = subject
        if predicate: params["predicate"] = predicate
        if object_val: params["object"] = object_val
        return self._get("/facts", params=params)

    def get_entity(self, entity):
        return self._get(f"/entities/{entity}")

    # --- Files ---
    def upload_file(self, filename, content, content_type="text/plain"):
        r = requests.post(
            self._url("/files"),
            headers={"Authorization": self.headers.get("Authorization", "")},
            files={"file": (filename, content, content_type)},
            timeout=60,
        )
        r.raise_for_status()
        return r.json().get("data")

    def download_file(self, file_id):
        r = requests.get(self._url(f"/files/{file_id}/download"),
                       headers=self.headers, timeout=60)
        r.raise_for_status()
        return r.content

    # --- Search ---
    def search(self, query, scope=None):
        return self._get("/search", params={"q": query})

    # --- Agents (inter-agent messaging) ---
    def send_message(self, to_agent, content, channel=None):
        return self._post("/agent-messages", {
            "to_agent": to_agent, "body": content,
            "channel": channel or "default",
        })

    def poll_inbox(self, limit=50):
        return self._get("/agent-messages/inbox", params={"limit": limit})

    # --- Planner ---
    def create_plan(self, title, steps=None):
        return self._post("/plans", {
            "name": title,
        })

    def get_plan(self, plan_id):
        return self._get(f"/plans/{plan_id}")

    # --- Tools ---
    def register_tool(self, name, description, endpoint=None, category="agent"):
        return self._post("/tools", {
            "name": name, "description": description,
            "endpoint": endpoint, "category": category,
        })

    def log_execution(self, tool_id, agent_id, input_data, output_data, status="success", duration_ms=0):
        return self._post(f"/tools/{tool_id}/execute", {
            "agent_id": agent_id, "input": input_data,
            "output": output_data, "status": status,
            "duration_ms": duration_ms,
        })

    # --- Health ---
    def health(self):
        r = requests.get(f"{self.base_url}/health", timeout=5)
        return r.status_code == 200
