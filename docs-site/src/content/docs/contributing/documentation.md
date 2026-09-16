---
title: Maintain these docs
description: Write help people can follow, keep the source of truth clear, and catch drift during the build.
sidebar:
  order: 3
---

These docs belong with the application because a feature change and its explanation need to travel together. The public site is a static build of this repository.

## Choose the right page

- **Start here** explains the product and first setup.
- **Install & maintain** covers deployment, networking, backups, and upgrades.
- **Use Cantinarr** helps people complete everyday tasks.
- **Manage your server** explains administrative decisions and their effects.
- **Connect your services** covers each integration.
- **Fix a problem** starts with an observable symptom.
- **Reference** records settings, contracts, and exact behavior.
- **Contribute** explains development, tests, and releases.

Extend an existing page when it answers the same task. Add a new page when the reader has a distinct problem or needs a separately linkable reference.

## Write for someone doing the work

Start with the outcome. Explain prerequisites before steps. Name the actual screen or control. After a meaningful action, say what success looks like and what a failure does or does not prove.

Use ordinary words before internal terminology. Explain an instance as one installation of a service before using it throughout a guide. Treat empty, unavailable, and stale as different states.

Do not use em dashes, invented success claims, filler introductions, or “just” and “simply” to dismiss a difficult step. Do not blame the reader for a confusing interface.

## Keep maintained references in one place

The build imports the API, app behavior, architecture, deployment-variable table, integration guides, privacy policy, and release playbook from their owning repository documents. It splits long references into navigable pages and normalizes internal links.

The settings catalog comes from the app's search registry. Do not edit generated copies under `reference/generated`, `integrations/guides`, or `contributing/generated`; the next build replaces them.

Edit the owning source and rebuild. Each imported page links to that source.

## Check the site

From `docs-site/`:

```sh
npm ci
npm run build
```

The build checks local links and fragments, page metadata, required topic coverage, source fingerprints, documented configuration names, tool coverage, and the search output. It rejects em dashes in page text.

External links are not required to be reachable during every build. Provider outages and rate limits should not prevent a valid local documentation change. Check changed external destinations deliberately when editing them.

## Document the right version

The site follows development main. Explain when stable installations may not yet contain a feature and direct readers to **Settings > About**. Do not claim that a merged change is already in a store or available on a device.

Deployment details and the local validation procedure are maintained in the [docs-site README](https://github.com/windoze95/cantinarr/blob/main/docs-site/README.md).
