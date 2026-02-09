const catalogItems = [
  {
    id: "workspaces",
    name: "Workspace",
    type: "Foundation",
    description: "Isolated tenant environments with policy enforcement and network boundaries.",
    resources: "Namespace, NetworkPolicy, Kyverno",
    status: "Ready",
  },
  {
    id: "webApplications",
    name: "WebApplication",
    type: "Runtime",
    description: "Deploy containerized applications with ingress, autoscaling, and DNS.",
    resources: "Deployment, Service, Gateway",
    status: "Ready",
  },
  {
    id: "stateStores",
    name: "StateStore",
    type: "Data",
    description: "Provision PostgreSQL and Redis with secrets and backups.",
    resources: "CloudNativePG, Redis",
    status: "Ready",
  },
  {
    id: "eventStreams",
    name: "EventStream",
    type: "Streaming",
    description: "Spin up Kafka clusters and topics for real-time workflows.",
    resources: "Strimzi Kafka, Topics",
    status: "Ready",
  },
  {
    id: "observability",
    name: "Observability",
    type: "Platform",
    description: "Logs, metrics, and traces out of the box with LGTM stack.",
    resources: "Loki, Grafana, Tempo, Mimir",
    status: "Connected",
  },
];

const docsLinks = [
  {
    title: "Getting Started",
    description: "Bootstrap a cluster and install platform addons.",
    link: "README.md",
  },
  {
    title: "Service Definitions",
    description: "Explore WebApplication, StateStore, and EventStream specs.",
    link: "3-user-space/team-a",
  },
  {
    title: "GitOps Workflow",
    description: "FluxCD sync rules and promotion flow.",
    link: "2-addons",
  },
  {
    title: "Observability",
    description: "Port-forward Grafana and explore dashboards.",
    link: "shoulders dashboard",
  },
];

const teams = [
  {
    name: "Team A",
    owner: "Sasha Nguyen",
    quota: "4 apps / 2 DBs",
    status: "Onboarded",
  },
  {
    name: "Platform Engineering",
    owner: "Miguel Alvarez",
    quota: "Shared",
    status: "Maintainers",
  },
  {
    name: "Growth",
    owner: "Priya Patel",
    quota: "3 apps / 1 DB",
    status: "Pending review",
  },
];

const catalogGrid = document.getElementById("catalog-grid");
const docsGrid = document.getElementById("docs-grid");
const teamsGrid = document.getElementById("teams-grid");
const apiWarning = document.getElementById("api-warning");
const catalogSearch = document.getElementById("catalog-search");
const catalogNamespace = document.getElementById("catalog-namespace");
const catalogRefresh = document.getElementById("catalog-refresh");
const contextSelect = document.getElementById("context-select");
const contextApply = document.getElementById("context-apply");

let latestSummary = null;

function setWarning(message) {
  if (!apiWarning) return;
  if (!message) {
    apiWarning.classList.add("hidden");
    apiWarning.textContent = "";
    return;
  }
  apiWarning.textContent = message;
  apiWarning.classList.remove("hidden");
}

function createCard(item, className) {
  const card = document.createElement("div");
  card.className = className;

  const meta = document.createElement("div");
  meta.className = "card-meta";
  meta.textContent = item.type || "Doc";

  const title = document.createElement("h3");
  title.textContent = item.name || item.title;

  const desc = document.createElement("p");
  desc.className = "muted";
  desc.textContent = item.description;

  card.append(meta, title, desc);

  if (item.resources) {
    const res = document.createElement("p");
    res.className = "mono muted";
    res.textContent = item.resources;
    card.append(res);
  }

  if (Number.isFinite(item.count)) {
    const count = document.createElement("p");
    count.className = "mono muted";
    count.textContent = `Instances: ${item.count}`;
    card.append(count);
  }

  if (item.instances && item.instances.length > 0) {
    const preview = document.createElement("div");
    preview.className = "note";
    preview.textContent = `Examples: ${item.instances.join(", ")}`;
    card.append(preview);
  }

  if (item.status) {
    const status = document.createElement("div");
    status.className = "pill";
    status.textContent = item.status;
    card.append(status);
  }

  if (item.link) {
    const link = document.createElement("div");
    link.className = "note";
    link.textContent = `Ref: ${item.link}`;
    card.append(link);
  }

  return card;
}

function renderCatalog(items) {
  catalogGrid.innerHTML = "";
  items.forEach((item) => {
    catalogGrid.append(createCard(item, "catalog-card fade-in"));
  });
}

function renderDocs() {
  docsLinks.forEach((doc) => {
    docsGrid.append(createCard(doc, "doc-card fade-in"));
  });
}

function renderTeams() {
  teams.forEach((team) => {
    const card = document.createElement("div");
    card.className = "team-card fade-in";

    const title = document.createElement("h3");
    title.textContent = team.name;

    const owner = document.createElement("p");
    owner.className = "muted";
    owner.textContent = `Owner: ${team.owner}`;

    const quota = document.createElement("p");
    quota.className = "mono muted";
    quota.textContent = `Quota: ${team.quota}`;

    const status = document.createElement("div");
    status.className = "pill";
    status.textContent = team.status;

    card.append(title, owner, quota, status);
    teamsGrid.append(card);
  });
}

async function fetchJson(url, options = {}) {
  try {
    const response = await fetch(url, options);
    if (!response.ok) throw new Error(`Request failed: ${response.status}`);
    return await response.json();
  } catch (error) {
    return null;
  }
}

function getFilters() {
  return {
    search: catalogSearch.value.trim().toLowerCase(),
    namespace: catalogNamespace.value,
  };
}

function filterResources(resources, filters) {
  const byNamespace = (item) =>
    !filters.namespace || item.namespace === filters.namespace;
  const bySearch = (item) =>
    !filters.search ||
    item.name.toLowerCase().includes(filters.search) ||
    item.namespace.toLowerCase().includes(filters.search);

  return resources.filter((item) => byNamespace(item) && bySearch(item));
}

function applyFilters(summary, filters) {
  if (!summary?.resources) return summary;

  const filtered = {
    workspaces: filterResources(summary.resources.workspaces || [], filters),
    webApplications: filterResources(summary.resources.webApplications || [], filters),
    stateStores: filterResources(summary.resources.stateStores || [], filters),
    eventStreams: filterResources(summary.resources.eventStreams || [], filters),
  };

  return {
    ...summary,
    counts: {
      workspaces: filtered.workspaces.length,
      webApplications: filtered.webApplications.length,
      stateStores: filtered.stateStores.length,
      eventStreams: filtered.eventStreams.length,
    },
    resources: filtered,
  };
}

function updateStats(summary) {
  const statWorkspaces = document.getElementById("stat-workspaces");
  const statApps = document.getElementById("stat-apps");
  const statStores = document.getElementById("stat-stores");
  const statStreams = document.getElementById("stat-streams");
  const statContext = document.getElementById("stat-context");

  if (summary?.counts) {
    statWorkspaces.textContent = summary.counts.workspaces ?? 0;
    statApps.textContent = summary.counts.webApplications ?? 0;
    statStores.textContent = summary.counts.stateStores ?? 0;
    statStreams.textContent = summary.counts.eventStreams ?? 0;
  }

  if (summary?.context) {
    statContext.textContent = summary.context;
  }
}

function mergeCatalog(summary) {
  if (!summary?.resources) return catalogItems;

  const map = {
    workspaces: summary.resources.workspaces || [],
    webApplications: summary.resources.webApplications || [],
    stateStores: summary.resources.stateStores || [],
    eventStreams: summary.resources.eventStreams || [],
  };

  return catalogItems.map((item) => {
    if (!map[item.id]) return item;
    const instances = map[item.id].map((entry) => entry.name).filter(Boolean);
    const preview = instances.slice(0, 3);
    return {
      ...item,
      count: instances.length,
      instances: preview,
    };
  });
}

async function loadSummary() {
  const summary = await fetchJson("/api/summary");
  if (!summary) {
    setWarning(
      "Kubernetes API not connected. Start the portal server with npm run dev to load live data."
    );
    renderCatalog(catalogItems);
    return;
  }

  latestSummary = summary;
  const filtered = applyFilters(summary, getFilters());
  updateStats(filtered);
  renderCatalog(mergeCatalog(filtered));

  if (summary.warnings && summary.warnings.length) {
    setWarning(`API warnings: ${summary.warnings.join(" | ")}`);
  } else {
    setWarning("");
  }
}

async function loadNamespaces() {
  const data = await fetchJson("/api/namespaces");
  if (!data || !Array.isArray(data.items)) return;

  const current = catalogNamespace.value;
  catalogNamespace.innerHTML = "<option value=\"\">All namespaces</option>";
  data.items
    .map((item) => item.name)
    .filter(Boolean)
    .sort()
    .forEach((name) => {
      const option = document.createElement("option");
      option.value = name;
      option.textContent = name;
      if (name === current) option.selected = true;
      catalogNamespace.append(option);
    });
}

async function loadContexts() {
  const data = await fetchJson("/api/contexts");
  if (!data || !Array.isArray(data.contexts)) return;

  contextSelect.innerHTML = "";
  data.contexts.forEach((contextName) => {
    const option = document.createElement("option");
    option.value = contextName;
    option.textContent = contextName;
    if (contextName === data.current) option.selected = true;
    contextSelect.append(option);
  });
}

const resourceKind = document.getElementById("resource-kind");
const namespaceField = document.getElementById("namespace-field");
const resourceNameField = document.getElementById("resource-name-field");
const resourceNameLabel = document.getElementById("resource-name-label");
const resourceNameNote = document.getElementById("resource-name-note");
const workspaceNamePreview = document.getElementById("workspace-name-preview");
const webappFields = document.getElementById("webapp-fields");
const webappScaleFields = document.getElementById("webapp-scale-fields");
const stateStoreFields = document.getElementById("state-store-fields");
const eventStreamFields = document.getElementById("event-stream-fields");
const deployForm = document.getElementById("deploy-form");
const yamlOutput = document.getElementById("yaml-output");
const copyYaml = document.getElementById("copy-yaml");
const applyYaml = document.getElementById("apply-yaml");
const applyStatus = document.getElementById("apply-status");

function toggleFields() {
  const kind = resourceKind.value;
  const isWebApp = kind === "WebApplication";
  const isWorkspace = kind === "Workspace";
  webappFields.classList.toggle("hidden", !isWebApp);
  webappScaleFields.classList.toggle("hidden", !isWebApp);
  namespaceField.classList.toggle("hidden", isWorkspace);
  if (resourceNameLabel && resourceNameNote) {
    resourceNameLabel.textContent = isWorkspace ? "Workspace Name" : "Resource Name";
    resourceNameNote.classList.toggle("hidden", !isWorkspace);
  }
  if (resourceNameField && workspaceNamePreview) {
    resourceNameField.classList.toggle("hidden", isWorkspace);
    workspaceNamePreview.classList.toggle("hidden", !isWorkspace);
    if (isWorkspace) {
      const generated = generateWorkspaceName();
      document.getElementById("resource-name").value = generated;
      workspaceNamePreview.textContent = `Workspace name will be: ${generated}`;
    } else {
      workspaceNamePreview.textContent = "";
    }
  }
  stateStoreFields.classList.toggle("hidden", kind !== "StateStore");
  eventStreamFields.classList.toggle("hidden", kind !== "EventStream");
}

function generateWorkspaceName() {
  const now = new Date();
  const stamp = `${now.getFullYear()}${String(now.getMonth() + 1).padStart(2, "0")}${String(
    now.getDate()
  ).padStart(2, "0")}-${String(now.getHours()).padStart(2, "0")}${String(
    now.getMinutes()
  ).padStart(2, "0")}${String(now.getSeconds()).padStart(2, "0")}`;
  return `workspace-${stamp}`;
}

function generateYaml() {
  const kind = resourceKind.value;
  const namespace = document.getElementById("resource-namespace").value.trim() || "default";
  const name = document.getElementById("resource-name").value.trim() || "new-resource";

  if (kind === "WebApplication") {
    const image = document.getElementById("resource-image").value.trim() || "nginx";
    const tag = document.getElementById("resource-tag").value.trim() || "latest";
    const replicas = document.getElementById("resource-replicas").value || "2";
    const host = document.getElementById("resource-host").value.trim() || "app.example.com";

    return `apiVersion: shoulders.io/v1alpha1\nkind: WebApplication\nmetadata:\n  name: ${name}\n  namespace: ${namespace}\nspec:\n  image: ${image}\n  tag: ${tag}\n  replicas: ${replicas}\n  host: ${host}\n`;
  }

  if (kind === "StateStore") {
    const database = document.getElementById("resource-database").value.trim() || "team-db";

    return `apiVersion: shoulders.io/v1alpha1\nkind: StateStore\nmetadata:\n  name: ${name}\n  namespace: ${namespace}\nspec:\n  database: ${database}\n`;
  }

  if (kind === "Workspace") {
    return `apiVersion: shoulders.io/v1alpha1\nkind: Workspace\nmetadata:\n  name: ${name}\nspec: {}\n`;
  }

  const topic = document.getElementById("resource-topic").value.trim() || "events";
  const partitions = document.getElementById("resource-partitions").value || "3";

  return `apiVersion: shoulders.io/v1alpha1\nkind: EventStream\nmetadata:\n  name: ${name}\n  namespace: ${namespace}\nspec:\n  topics:\n    - name: ${topic}\n      partitions: ${partitions}\n      config:\n        retention.ms: "604800000"\n`;
}

resourceKind.addEventListener("change", () => {
  toggleFields();
  yamlOutput.textContent = generateYaml();
});

deployForm.addEventListener("submit", (event) => {
  event.preventDefault();
  yamlOutput.textContent = generateYaml();
});

copyYaml.addEventListener("click", async () => {
  const text = yamlOutput.textContent.trim();
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
    copyYaml.textContent = "Copied";
    setTimeout(() => {
      copyYaml.textContent = "Copy";
    }, 1500);
  } catch (error) {
    copyYaml.textContent = "Copy failed";
    setTimeout(() => {
      copyYaml.textContent = "Copy";
    }, 1500);
  }
});

applyYaml.addEventListener("click", async () => {
  const yaml = yamlOutput.textContent.trim();
  if (!yaml) return;
  applyStatus.textContent = "Applying...";

  const result = await fetchJson("/api/apply", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ yaml }),
  });

  if (!result) {
    applyStatus.textContent = "Apply failed. Ensure the API server is running.";
    return;
  }

  if (result.errors && result.errors.length) {
    applyStatus.textContent = `Apply errors: ${result.errors.join(" | ")}`;
    return;
  }

  const appliedCount = result.applied ? result.applied.length : 0;
  applyStatus.textContent = `Applied ${appliedCount} resource(s).`;
  loadSummary();
});

catalogSearch.addEventListener("input", () => {
  if (!latestSummary) return;
  const filtered = applyFilters(latestSummary, getFilters());
  updateStats(filtered);
  renderCatalog(mergeCatalog(filtered));
});

catalogNamespace.addEventListener("change", () => {
  if (!latestSummary) return;
  const filtered = applyFilters(latestSummary, getFilters());
  updateStats(filtered);
  renderCatalog(mergeCatalog(filtered));
});

catalogRefresh.addEventListener("click", () => {
  loadSummary();
  loadNamespaces();
});

contextApply.addEventListener("click", async () => {
  const context = contextSelect.value;
  if (!context) return;
  contextApply.textContent = "Switching...";
  const result = await fetchJson("/api/context", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ context }),
  });

  if (!result || result.error) {
    setWarning(result?.error || "Failed to switch context.");
  } else {
    setWarning("");
  }
  contextApply.textContent = "Use Context";
  await loadSummary();
  await loadNamespaces();
});

const grafanaButton = document.getElementById("load-grafana");
const grafanaEmbed = document.getElementById("grafana-embed");
const grafanaUrlInput = document.getElementById("grafana-url");

grafanaButton.addEventListener("click", () => {
  const url = grafanaUrlInput.value.trim();
  if (!url) return;

  grafanaEmbed.innerHTML = `\n    <iframe\n      title="Grafana Dashboard"\n      src="${url}"\n      style="width:100%; height:100%; border:0;"\n      loading="lazy"\n    ></iframe>\n  `;
});

const inviteButton = document.getElementById("send-invite");
const inviteStatus = document.getElementById("invite-status");

inviteButton.addEventListener("click", () => {
  const email = document.getElementById("invite-email").value.trim();
  const role = document.getElementById("invite-role").value;
  if (!email) {
    inviteStatus.textContent = "Enter an email to send an invite.";
    return;
  }
  inviteStatus.textContent = `Invite sent to ${email} (${role}).`;
});

const navLinks = document.querySelectorAll(".nav-link");

function setActiveLink() {
  const hash = window.location.hash || "#overview";
  navLinks.forEach((link) => {
    link.classList.toggle("active", link.getAttribute("href") === hash);
  });
}

navLinks.forEach((link) => {
  link.addEventListener("click", () => {
    setTimeout(setActiveLink, 0);
  });
});

window.addEventListener("hashchange", setActiveLink);

const jumpButtons = document.querySelectorAll("[data-jump]");

jumpButtons.forEach((button) => {
  button.addEventListener("click", () => {
    const target = button.getAttribute("data-jump");
    if (target) window.location.hash = target;
  });
});

const statusButton = document.getElementById("toggle-status");
const statuses = [
  "Platform: Healthy",
  "Platform: Scaling",
  "Platform: Deploying",
];
let statusIndex = 0;

statusButton.addEventListener("click", () => {
  statusIndex = (statusIndex + 1) % statuses.length;
  statusButton.textContent = statuses[statusIndex];
});

function init() {
  toggleFields();
  yamlOutput.textContent = generateYaml();
  setActiveLink();
  renderDocs();
  renderTeams();
  renderCatalog(catalogItems);
  loadContexts();
  loadNamespaces();
  loadSummary();
}

init();
