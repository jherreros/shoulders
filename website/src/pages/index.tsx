import type {ReactNode} from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';

import styles from './index.module.css';

function Terminal(): ReactNode {
  return (
    <div className={styles.terminal}>
      <div className={styles.termBar}>
        <span className={styles.dot} style={{background: '#ff5f57'}} />
        <span className={styles.dot} style={{background: '#febc2e'}} />
        <span className={styles.dot} style={{background: '#28c840'}} />
        <span className={styles.termTitle}>terminal — zsh</span>
      </div>
      <div className={styles.termBody}>
        <div><span className={styles.prompt}>$ </span>brew install jherreros/tap/shoulders</div>
        <div><span className={styles.prompt}>$ </span>shoulders up</div>
        <div className={styles.termOk}>✓ vind cluster “shoulders” ready (control-plane + 2 workers)</div>
        <div className={styles.termOk}>✓ FluxCD bootstrapped — 24 HelmReleases reconciled</div>
        <div className={styles.termOk}>✓ Crossplane XRDs established — platform API live</div>
        <div><span className={styles.prompt}>$ </span>shoulders workspace create team-a</div>
        <div><span className={styles.prompt}>$ </span>shoulders app init checkout --image my-registry/checkout:v2</div>
        <div className={styles.termOk}>✓ checkout.team-a live at https://checkout.example.com</div>
      </div>
    </div>
  );
}

const stack = [
  'Crossplane', 'FluxCD', 'Cilium', 'Gateway API', 'Strimzi Kafka',
  'CloudNativePG', 'Prometheus', 'Grafana', 'Loki', 'Tempo',
  'Kyverno', 'Trivy', 'Falco', 'Dex', 'Headlamp', 'Garage S3',
];

const features = [
  {
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
        <path d="M13 2 4.5 13.5H11L10 22l8.5-11.5H12L13 2z" strokeLinejoin="round" />
      </svg>
    ),
    title: 'Self-service in seconds',
    text: 'Developers ship WebApplications, workers, jobs and cronjobs from one declarative API — no YAML archaeology, no tickets to platform teams.',
  },
  {
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
        <ellipse cx="12" cy="5.5" rx="7" ry="2.8" />
        <path d="M5 5.5v6c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8v-6" />
        <path d="M5 11.5v6c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8v-6" />
      </svg>
    ),
    title: 'Data & streaming included',
    text: 'PostgreSQL, Redis, S3-compatible buckets and full Kafka clusters provisioned with the same workflow as the app itself.',
  },
  {
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
        <path d="M3 12h4l2.5-6 4 12L16 12h5" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    ),
    title: 'Observability from day zero',
    text: 'Metrics, logs and traces wired automatically through Prometheus, Loki, Tempo and Grafana. Every app is born observable.',
  },
  {
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
        <path d="M12 3l7 3v5c0 5-3 8-7 10-4-2-7-5-7-10V6l7-3z" strokeLinejoin="round" />
        <path d="M9.5 12l2 2 3.5-4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    ),
    title: 'Secure & compliant by default',
    text: 'Network policies, admission guardrails, vulnerability scanning and runtime threat detection — auditable in one reporter UI.',
  },
  {
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
        <path d="M12 3v12m0 0l-4-4m4 4l4-4" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M4 15v4a2 2 0 002 2h12a2 2 0 002-2v-4" strokeLinecap="round" />
      </svg>
    ),
    title: 'GitOps under the hood',
    text: 'The entire platform reconciles from git via FluxCD — with OCI snapshots for dirty-tree iteration and airgap bundles for offline installs.',
  },
  {
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
        <rect x="5" y="5" width="14" height="14" rx="2" />
        <path d="M9 9h6v6H9z" />
        <path d="M12 2v3m0 14v3M2 12h3m14 0h3" strokeLinecap="round" />
      </svg>
    ),
    title: 'AI-native operations',
    text: 'An MCP server exposes the whole platform to AI assistants, plus a Headlamp portal for humans and a CLI for automation.',
  },
];

const steps = [
  {
    num: '01',
    title: 'Boot the platform',
    text: 'One command creates the cluster and installs 20+ components via GitOps — networking, identity, data, observability, security.',
    code: 'shoulders up',
  },
  {
    num: '02',
    title: 'Give teams a workspace',
    text: 'Isolated namespaces with network policy and naming guardrails. Developers self-serve from there — no cluster-admin required.',
    code: 'shoulders workspace create team-a\nshoulders app init checkout --image my-registry/checkout:v2',
  },
  {
    num: '03',
    title: 'Observe everything',
    text: 'Dashboards, logs, traces and compliance reports are already wired. Open Grafana or the portal and start operating.',
    code: 'shoulders dashboard\nshoulders portal',
  },
];

const metrics = [
  {value: '20+', label: 'integrated open-source components'},
  {value: '5', label: 'developer-facing APIs (XRDs)'},
  {value: '3', label: 'interfaces: CLI · portal · MCP'},
  {value: '1', label: 'command to a running platform'},
];

export default function Home(): ReactNode {
  return (
    <Layout
      title="Shoulders — the Internal Developer Platform that runs itself"
      description="Shoulders is an open-source Internal Developer Platform on Kubernetes: one command to a complete platform with apps, data, streaming, observability and security.">
      {/* HERO */}
      <header className={styles.hero}>
        <div className="container">
          <div className={styles.heroGrid}>
            <div>
              <div className={styles.badge}>Open source · Kubernetes-native IDP</div>
              <Heading as="h1" className={styles.heroTitle}>
                All-in-one developer platform,<br />
                <span className={styles.gradient}>in a single command.</span>
              </Heading>
              <p className={styles.heroSubtitle}>
                Shoulders turns Kubernetes into a self-service Internal Developer Platform:
                apps, databases, Kafka, observability, security and GitOps — composed from
                best-in-class open source, ready after <code>shoulders up</code>.
              </p>
              <div className={styles.ctaRow}>
                <Link className={styles.ctaPrimary} to="/docs/getting-started/quickstart">
                  Get started
                </Link>
                <Link className={styles.ctaSecondary} href="https://github.com/jherreros/shoulders">
                  Star on GitHub
                </Link>
              </div>
              <div className={styles.metrics}>
                {metrics.map((m) => (
                  <div key={m.label} className={styles.metric}>
                    <div className={styles.metricValue}>{m.value}</div>
                    <div className={styles.metricLabel}>{m.label}</div>
                  </div>
                ))}
              </div>
            </div>
            <Terminal />
          </div>
        </div>
      </header>

      <main>
        {/* STACK */}
        <section className={styles.stackSection}>
          <div className="container">
            <p className={styles.kicker}>Standing on the shoulders of giants</p>
            <div className={styles.pills}>
              {stack.map((s) => (
                <span key={s} className={styles.pill}>{s}</span>
              ))}
            </div>
          </div>
        </section>

        {/* PROBLEM → SOLUTION */}
        <section className="container margin-vert--xl">
          <p className={styles.kicker}>Why Shoulders exists</p>
          <div className={styles.twoCol}>
            <div className={styles.problem}>
              <Heading as="h3">Platform engineering is expensive</Heading>
              <p>
                Every company re-builds the same stack: clusters, networking, databases,
                Kafka, monitoring, security policies, developer portals. Months of
                undifferentiated work — and developers still wait on tickets to ship.
              </p>
            </div>
            <div className={styles.solution}>
              <Heading as="h3">A reference platform, ready to run</Heading>
              <p>
                Shoulders packages those decisions into an opinionated, production-shaped
                platform: Crossplane APIs for developers, FluxCD reconciliation for
                operators, and small/medium/large profiles plus airgap support for
                real-world constraints.
              </p>
            </div>
          </div>
        </section>

        {/* FEATURES */}
        <section className="container margin-vert--xl">
          <p className={styles.kicker}>What teams get</p>
          <Heading as="h2" className={styles.sectionTitle}>
            Everything around the app, included
          </Heading>
          <div className={styles.featureGrid}>
            {features.map((f) => (
              <div key={f.title} className={styles.featureCard}>
                <div className={styles.featureIcon}>{f.icon}</div>
                <h3>{f.title}</h3>
                <p>{f.text}</p>
              </div>
            ))}
          </div>
        </section>

        {/* HOW IT WORKS */}
        <section className={styles.howSection}>
          <div className="container">
            <p className={styles.kicker}>How it works</p>
            <Heading as="h2" className={styles.sectionTitle}>
              From zero to developer self-service in three steps
            </Heading>
            <div className={styles.steps}>
              {steps.map((s) => (
                <div key={s.num} className={styles.stepCard}>
                  <div className={styles.stepNum}>{s.num}</div>
                  <h3>{s.title}</h3>
                  <p>{s.text}</p>
                  <pre className={styles.stepCode}><code>{s.code}</code></pre>
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* PROFILES */}
        <section className="container margin-vert--xl">
          <p className={styles.kicker}>Fits your constraints</p>
          <Heading as="h2" className={styles.sectionTitle}>
            One platform, three footprints
          </Heading>
          <div className={styles.profileGrid}>
            <div className={styles.profileCard}>
              <h3>Small</h3>
              <p className={styles.profileTag}>Laptops &amp; small clusters</p>
              <p>Core IDP, Prometheus + Grafana, Dex, Headlamp, PostgreSQL, S3. No event streams, no heavy scanners.</p>
            </div>
            <div className={`${styles.profileCard} ${styles.profileFeatured}`}>
              <div className={styles.profileFlag}>Default</div>
              <h3>Medium</h3>
              <p className={styles.profileTag}>The full local platform</p>
              <p>Adds Kafka, Loki/Tempo/Alloy, Trivy, Falco and Policy Reporter on 2 workers.</p>
            </div>
            <div className={styles.profileCard}>
              <h3>Large</h3>
              <p className={styles.profileTag}>Maximum headroom</p>
              <p>Full feature set on 3 workers with longer Prometheus retention.</p>
            </div>
          </div>
          <p>
            Plus OCI snapshot iteration for contributors and single-file airgap bundles —{' '}
            <Link to="/docs/guides/profiles">profiles guide</Link> ·{' '}
            <Link to="/docs/guides/airgap">airgap</Link> ·{' '}
            <Link to="/docs/guides/local-iteration">local loop</Link>
          </p>
        </section>

        {/* QUOTE */}
        <section className={styles.quoteBand}>
          <div className="container">
            <blockquote>
              “If I have seen further it is by standing on the shoulders of Giants.”
              <cite>— Isaac Newton, and the reason this platform is called Shoulders</cite>
            </blockquote>
          </div>
        </section>

        {/* FINAL CTA */}
        <section className="container margin-vert--xl">
          <div className={styles.ctaBand}>
            <Heading as="h2" id="quickstart">
              Your platform is one command away.
            </Heading>
            <p>Open source (MIT). Runs on your laptop today, on your cluster tomorrow.</p>
            <div className={styles.ctaRow}>
              <Link className={styles.ctaPrimary} to="/docs/getting-started/quickstart">
                Read the quickstart
              </Link>
              <Link className={styles.ctaSecondaryDark} href="https://github.com/jherreros/shoulders">
                Browse the code
              </Link>
            </div>
          </div>
        </section>
      </main>
    </Layout>
  );
}
