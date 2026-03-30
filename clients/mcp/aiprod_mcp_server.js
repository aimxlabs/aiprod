#!/usr/bin/env node
/**
 * aiprod MCP Server — Exposes aiprod productivity tools via the Model Context Protocol.
 * Runs as a stdio MCP server that agents can use for memory, docs, knowledge, tasks, etc.
 */

const http = require('http');
const readline = require('readline');

const AIPROD_URL = process.env.AIPROD_URL || 'http://agentkit-aiprod:8600';
const AIPROD_KEY = process.env.AIPROD_API_KEY || '';

// --- HTTP helpers ---
function apiRequest(method, path, body) {
    return new Promise((resolve, reject) => {
        const url = new URL(`${AIPROD_URL}/api/v1${path}`);
        const options = {
            hostname: url.hostname,
            port: url.port,
            path: url.pathname + url.search,
            method,
            headers: {
                'Content-Type': 'application/json',
                ...(AIPROD_KEY ? { 'Authorization': `Bearer ${AIPROD_KEY}` } : {}),
            },
            timeout: 30000,
        };
        const req = http.request(options, (res) => {
            let data = '';
            res.on('data', c => data += c);
            res.on('end', () => {
                try {
                    const json = JSON.parse(data);
                    resolve(json.data);
                } catch(e) { resolve(data); }
            });
        });
        req.on('error', reject);
        req.on('timeout', () => { req.destroy(); reject(new Error('timeout')); });
        if (body) req.write(JSON.stringify(body));
        req.end();
    });
}
const get = (path) => apiRequest('GET', path);
const post = (path, body) => apiRequest('POST', path, body);
const patch = (path, body) => apiRequest('PATCH', path, body);

// --- Tool definitions ---
const TOOLS = [
    {
        name: 'memory_store',
        description: 'Store information in persistent memory for later recall.',
        inputSchema: {
            type: 'object',
            properties: {
                namespace: { type: 'string', description: 'Memory category (e.g. user-preferences, research-findings)' },
                key: { type: 'string', description: 'Short identifier for this memory' },
                value: { type: 'string', description: 'Content to remember' },
            },
            required: ['namespace', 'key', 'value'],
        },
    },
    {
        name: 'memory_recall',
        description: 'Search and recall stored memories.',
        inputSchema: {
            type: 'object',
            properties: {
                namespace: { type: 'string', description: 'Memory category to search (optional)' },
                query: { type: 'string', description: 'Search query (optional)' },
            },
        },
    },
    {
        name: 'create_document',
        description: 'Create a persistent, versioned document (report, summary, notes).',
        inputSchema: {
            type: 'object',
            properties: {
                title: { type: 'string', description: 'Document title' },
                content: { type: 'string', description: 'Document content (markdown)' },
                tags: { type: 'array', items: { type: 'string' }, description: 'Tags' },
            },
            required: ['title', 'content'],
        },
    },
    {
        name: 'search_documents',
        description: 'Search across stored documents.',
        inputSchema: {
            type: 'object',
            properties: {
                query: { type: 'string', description: 'Search query' },
            },
            required: ['query'],
        },
    },
    {
        name: 'add_knowledge',
        description: 'Store a verified fact as a knowledge triple (subject-predicate-object).',
        inputSchema: {
            type: 'object',
            properties: {
                subject: { type: 'string', description: 'The entity' },
                predicate: { type: 'string', description: 'The relationship' },
                object: { type: 'string', description: 'The value' },
                confidence: { type: 'number', description: 'Confidence 0.0-1.0' },
            },
            required: ['subject', 'predicate', 'object'],
        },
    },
    {
        name: 'query_knowledge',
        description: 'Query the knowledge graph for facts.',
        inputSchema: {
            type: 'object',
            properties: {
                subject: { type: 'string', description: 'Entity to look up' },
                predicate: { type: 'string', description: 'Relationship filter' },
            },
        },
    },
    {
        name: 'create_task',
        description: 'Create a task to track work.',
        inputSchema: {
            type: 'object',
            properties: {
                title: { type: 'string', description: 'Task title' },
                description: { type: 'string', description: 'Task details' },
                assignee: { type: 'string', description: 'Agent or person to assign the task to' },
                priority: { type: 'string', description: 'Priority: low, medium, high, critical' },
                due_date: { type: 'string', description: 'Due date (RFC3339 or YYYY-MM-DD)' },
                parent_id: { type: 'string', description: 'Parent task ID for subtasks' },
                tags: { type: 'array', items: { type: 'string' }, description: 'Tags for categorization' },
            },
            required: ['title'],
        },
    },
    {
        name: 'update_task',
        description: 'Update a task\'s fields (title, description, assignee, priority, due_date, tags).',
        inputSchema: {
            type: 'object',
            properties: {
                task_id: { type: 'string', description: 'Task ID to update' },
                title: { type: 'string', description: 'New title' },
                description: { type: 'string', description: 'New description' },
                assignee: { type: 'string', description: 'New assignee' },
                priority: { type: 'string', description: 'New priority: low, medium, high, critical' },
                due_date: { type: 'string', description: 'New due date' },
                tags: { type: 'array', items: { type: 'string' }, description: 'New tags' },
            },
            required: ['task_id'],
        },
    },
    {
        name: 'transition_task',
        description: 'Change a task\'s status. Valid transitions: open→in_progress/cancelled, in_progress→review/blocked/done/cancelled, blocked→in_progress/cancelled, review→in_progress/done/cancelled, done→open, cancelled→open.',
        inputSchema: {
            type: 'object',
            properties: {
                task_id: { type: 'string', description: 'Task ID' },
                status: { type: 'string', description: 'New status: open, in_progress, blocked, review, done, cancelled' },
            },
            required: ['task_id', 'status'],
        },
    },
    {
        name: 'list_tasks',
        description: 'List tasks, optionally filtered by status.',
        inputSchema: {
            type: 'object',
            properties: {
                status: { type: 'string', description: 'Filter by status (open, in_progress, done)' },
            },
        },
    },
    {
        name: 'dream',
        description: 'Run a memory maintenance cycle: consolidate related memories, decay old ones, prune low-importance entries, re-embed missing vectors, and generate a reflection. Use this when you notice your memories are getting cluttered or contradictory.',
        inputSchema: {
            type: 'object',
            properties: {},
        },
    },
];

// --- Tool execution ---
async function executeTool(name, args) {
    switch (name) {
        case 'memory_store':
            await post('/memory', { namespace: args.namespace, key: args.key, content: args.value });
            return `Stored: [${args.namespace}] ${args.key}`;

        case 'memory_recall': {
            const params = new URLSearchParams();
            if (args.namespace) params.set('namespace', args.namespace);
            if (args.query) params.set('q', args.query);
            params.set('limit', '20');
            const memories = await get(`/memory?${params}`);
            if (!memories || (Array.isArray(memories) && memories.length === 0)) return 'No memories found.';
            return JSON.stringify(memories, null, 2);
        }

        case 'create_document': {
            const doc = await post('/docs', { title: args.title, content: args.content, tags: args.tags || [] });
            return `Document created: ${args.title} (id: ${doc?.id || 'unknown'})`;
        }

        case 'search_documents': {
            const results = await get(`/search?q=${encodeURIComponent(args.query)}`);
            if (!results) return 'No results found.';
            return JSON.stringify(results, null, 2);
        }

        case 'add_knowledge': {
            await post('/facts', { subject: args.subject, predicate: args.predicate, object: args.object, confidence: args.confidence || 1.0 });
            return `Stored: ${args.subject} ${args.predicate} ${args.object}`;
        }

        case 'query_knowledge': {
            const params = new URLSearchParams();
            if (args.subject) params.set('subject', args.subject);
            if (args.predicate) params.set('predicate', args.predicate);
            const facts = await get(`/facts?${params}`);
            if (!facts || (Array.isArray(facts) && facts.length === 0)) return 'No facts found.';
            return JSON.stringify(facts, null, 2);
        }

        case 'create_task': {
            const body = { title: args.title, description: args.description || '' };
            if (args.assignee) body.assignee = args.assignee;
            if (args.priority) body.priority = args.priority;
            if (args.due_date) body.due_date = args.due_date;
            if (args.parent_id) body.parent_id = args.parent_id;
            if (args.tags) body.tags = args.tags;
            const task = await post('/tasks', body);
            return `Task created: ${args.title} (id: ${task?.id || 'unknown'})`;
        }

        case 'update_task': {
            const body = {};
            for (const key of ['title', 'description', 'assignee', 'priority', 'due_date', 'tags']) {
                if (args[key] !== undefined) body[key] = args[key];
            }
            const task = await patch(`/tasks/${args.task_id}`, body);
            return `Task updated: ${args.task_id}`;
        }

        case 'transition_task': {
            const task = await post(`/tasks/${args.task_id}/transition`, { status: args.status });
            return `Task ${args.task_id} transitioned to ${args.status}`;
        }

        case 'list_tasks': {
            const params = args.status ? `?status=${args.status}` : '';
            const tasks = await get(`/tasks${params}`);
            if (!tasks || (Array.isArray(tasks) && tasks.length === 0)) return 'No tasks found.';
            return JSON.stringify(tasks, null, 2);
        }

        case 'dream': {
            const result = await post('/memory/dream', {});
            return JSON.stringify(result, null, 2);
        }

        default:
            return `Unknown tool: ${name}`;
    }
}

// --- MCP stdio protocol ---
const rl = readline.createInterface({ input: process.stdin });

function send(msg) {
    const json = JSON.stringify(msg);
    process.stdout.write(`Content-Length: ${Buffer.byteLength(json)}\r\n\r\n${json}`);
}

let buffer = '';

process.stdin.on('data', (chunk) => {
    buffer += chunk.toString();

    while (true) {
        const headerEnd = buffer.indexOf('\r\n\r\n');
        if (headerEnd === -1) break;

        const header = buffer.substring(0, headerEnd);
        const match = header.match(/Content-Length:\s*(\d+)/i);
        if (!match) { buffer = buffer.substring(headerEnd + 4); continue; }

        const len = parseInt(match[1]);
        const bodyStart = headerEnd + 4;
        if (buffer.length < bodyStart + len) break;

        const body = buffer.substring(bodyStart, bodyStart + len);
        buffer = buffer.substring(bodyStart + len);

        try {
            handleMessage(JSON.parse(body));
        } catch(e) {
            process.stderr.write(`[aiprod-mcp] Parse error: ${e.message}\n`);
        }
    }
});

async function handleMessage(msg) {
    if (msg.method === 'initialize') {
        send({
            jsonrpc: '2.0', id: msg.id,
            result: {
                protocolVersion: '2024-11-05',
                serverInfo: { name: 'aiprod', version: '1.0.0' },
                capabilities: { tools: {} },
            },
        });
    } else if (msg.method === 'notifications/initialized') {
        // No response needed
    } else if (msg.method === 'tools/list') {
        send({
            jsonrpc: '2.0', id: msg.id,
            result: { tools: TOOLS },
        });
    } else if (msg.method === 'tools/call') {
        const { name, arguments: args } = msg.params;
        try {
            const result = await executeTool(name, args || {});
            send({
                jsonrpc: '2.0', id: msg.id,
                result: { content: [{ type: 'text', text: String(result) }] },
            });
        } catch(e) {
            send({
                jsonrpc: '2.0', id: msg.id,
                result: { content: [{ type: 'text', text: `Error: ${e.message}` }], isError: true },
            });
        }
    } else if (msg.id) {
        send({ jsonrpc: '2.0', id: msg.id, error: { code: -32601, message: 'Method not found' } });
    }
}

process.stderr.write('[aiprod-mcp] Server started\n');
