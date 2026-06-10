# Scaffold — plan d'amélioration

Plan pour rendre le scaffolder **flexible sans jamais éditer du code généré**.
Ancré sur le code réel (`internal/generator/generator.go`, `templates.go`).

## Constat : 3 mécanismes de "preservation" coexistent

| Mécanisme | Implémentation | Fichiers concernés |
|---|---|---|
| **full-regen** | `writeGoFile(overwrite=true)` | `*_service_gen.go`, `*_store_gen.go`, `*_handler_gen.go`, **`registry.go`** (`writeRegistry` :931), **views** (`writeSSRTemplates` :876) |
| **write-once** | `writeGoFileOnce` (:474) | `ports/*.go`, `*_service.go`, `*_store.go`, `*_handler.go`, grpc `shared.go` |
| **markers** | `patchDomainMarkers` (:619) + `replaceMarkerBlock` (:1135) | `domain/*.go` (champs + GetID/WithID), `app.go` (bloc routes, `writeRoutes` :961), `schema.sql` |

Le split **`_gen.go` / write-once** est la bonne idée et marche bien. Les **markers
sont le maillon faible** et les **trous de seams** forcent à contourner.

## Principe directeur

> **On n'édite JAMAIS un fichier généré.** La flexibilité vient de *seams*
> (fonctions `Register…` générées + composition dans des fichiers write-once),
> pas de l'édition de code généré ni de markers fragiles.

Cible : **deux buckets uniformes** — `*_gen.go` (régénérés) et fichiers write-once
(jamais touchés). Retirer les markers. Tout ce qui est "custom" se compose dans
du write-once qui *appelle* le généré.

---

## P0 — ✅ CORRIGÉ — collision de versions de migration (vécu, panique au boot)

> Fix livré : `LoadManifest` scanne le dossier migrations (`highestMigrationOnDisk`)
> et pose un `migrationFloor` (champ non-persisté) ; `nextMigrationVersion` prend
> `max(base, compteur manifest, floor) + 1`. Tests : `migration_test.go`.


`scaffold gen` numérote les migrations depuis le compteur `migrationVersion` du
manifest (`.scaffold/models.json`), **sans scanner le dossier `migrations/`**.
Si des migrations existent hors de ce compteur (vécu : `00003_create_categories`,
`00004_drop_categories` côté projet), gen crée `00003_*` / `00004_*` en **double**
→ **goose panique au démarrage** : `duplicate version 3 detected`.

**Fix.** Dériver le prochain numéro en **scannant `migrations/`** (max réel + 1),
pas le compteur manifest ; **ou** passer à des versions **timestamp**
(`20060102150405_*`, supportées par goose) pour éliminer les collisions par
construction. Idéalement : détecter les doublons et refuser de générer avec un
message clair.

*(Contournement appliqué dans le projet : renommage en `00005/00006` + bump du
compteur à 6.)*

**Effort** : faible. **Impact** : critique (sinon l'app ne démarre pas).

## P1 — ✅ FAIT (tous les modes) — seam de wiring pour services custom

> **Découverte clé :** `NewRegistry(db, logger)` n'a **pas** accès à `cfg` (clés
> API), donc le bon seam n'est pas dans le registry mais au niveau **App** (qui a
> `cfg`). C'est exactement le pattern qu'on avait fait à la main (`app/roadmap.go`).
>
> Fix livré : fichier **write-once** partagé `internal/app/custom.go`
> (`static/internal/app/custom.go.tmpl`, donc présent dans tous les modes) avec un
> type `Custom` + `newCustom(cfg, reg, logger)` ; les **3** `app.go.tmpl` (ssr,
> sqlite, postgres → couvrent SSR/REST/gRPC) gagnent un champ `custom` câblé dans
> `NewApp`. Les services custom ont accès à `cfg` ET au registry, sans jamais
> éditer de code généré.
>
> Vérifié — compile sur tous les modes : `TestCompile_{SSR,REST}_{SQLite,Postgres}`
> + `TestCompile_GRPC_SQLite`.

### (Référence) idée initiale — seam dans `registry.go`

**Problème.** `writeRegistry` (:931) régénère `registry.go` en entier via
`writeGoFile(overwrite=true)`. Aucun point d'extension → impossible de câbler un
service non-model (vécu : il a fallu créer un `app/roadmap.go` à la main qui
consomme `reg.Stores.Step`).

**Fix.** Scinder :
- `registry_gen.go` (régénéré) : `Stores`/`Services`/`Handlers` des models + `NewRegistry`.
- `wiring.go` **write-once** : `func (r *Registry) wireCustom()` (ou un `Custom`
  struct que l'utilisateur remplit), appelé par `NewRegistry` à la fin.

Ainsi le wiring custom vit dans du write-once, et `registry_gen.go` reste
pleinement régénérable. *(C'est exactement le seam qu'on a recréé à la main.)*

**Touche** : `writeRegistry`, templates registry (`templates.go`), + 1 template write-once.
**Effort** : moyen. **Impact** : élevé. → **à faire en premier.**

## P2 — ✅ FAIT (tous les modes) — contrôle granulaire des opérations (`--only` / `--skip`)

> Remplace le contournement middleware (`DisableRoutes`) par du natif :
> `scaffold gen Model … --skip create,delete` ou `--only list,read`.
> Ops = `list, read, create, update, delete`.
>
> Socle : type `parser.Ops` + `ResolveOps`/`OpsFromSkipped` ; ops persistées dans
> le manifest (`skippedOps`, préservées au re-gen) ; flags `--skip`/`--only`
> (mutuellement exclusifs) dans `gen.go`.
>
> - **SSR** : gating de `RegisterRoutes` (handler par-model) + affordances de vues
>   (bouton New, actions View/Edit/Delete).
> - **REST** : handler CRUD générique → champ `CRUDOps` (gating runtime de
>   `RegisterRoutes`) ; le registry passe les ops par-model.
> - **gRPC** : RPC du proto **et** méthodes du handler gated de façon cohérente
>   (le handler implémente exactement le service du proto).
>
> Vérifié : `ResolveOps`/round-trip (parser) ; gating texte SSR/REST/gRPC
> (`TestScaffold_SkipsOps{,_REST,_GRPC}`) ; compile end-to-end
> `TestCompile_SSR_SkipOps` + baselines REST/SSR/gRPC inchangées.
> (gRPC : compile complet non vérifié en test — nécessite `make proto`/buf.)

## P3 — ✅ FAIT — views write-once (protégées par défaut)

> **Problème.** `writeSSRTemplates` régénérait les `.templ` à chaque gen → risque
> d'écraser des vues personnalisées (l'artefact le plus custom).
>
> **Fix livré.** Les views sont **write-once** : créées une fois (donc tout
> compile — le handler SSR appelle `views.XList/XForm/XShow`), puis **jamais
> réécrites** → elles t'appartiennent. `--regen-views` force un scaffold frais.
> (`Generator.RegenViews` ; gating dans `writeSSRTemplates`.)
>
> Choix : write-once plutôt que « pas de views du tout » à cause du couplage
> handler↔views (sinon le projet ne compile pas). Pas de `--no-views` séparé —
> pour zéro UI scaffold sur un model, `--no-handler` skippe déjà handler+views.
>
> Vérifié : `TestScaffold_SSR_Views_WriteOnce` (1er gen crée ; re-gen ne touche
> pas ; `--regen-views` rafraîchit) ; compile tests SSR inchangés.

## P4 — ✅ FAIT — retirer les markers (modèle 2 buckets uniforme)

> **Domaine (P4).** `domain/{model}.go` à markers scindé en :
> - `domain/{model}_gen.go` — struct + `GetID`/`WithID`, régénéré, **sans markers**.
> - `domain/{model}.go` — `Validate()` + méthodes custom, **write-once**.
>
> `patchDomainMarkers` + `domainTmpl`/`domainTmplPostgres` supprimés ; `Destroy`
> retire les deux. Tests : `TestScaffold_DomainSplit`, `StructPacking`, `REST_Domain`.
>
> **Routes (P4b).** Le bloc `scaffold:routes`/`scaffold:grpc` dans `app.go` est
> remplacé par un **`internal/app/routes_gen.go`** régénéré (`routesGenTmpl` ;
> `(a *App) registerGeneratedRoutes(r)` + `registerGeneratedGRPC()` en mode gRPC).
> `app.go` (boilerplate hand-written, 3 modes) **appelle** ces fonctions — plus
> aucun marker. `routes_gen.go` initial (vide) shippé dans `static/`. `writeRoutes`
> n'ouvre plus `app.go`. Tests : `TestCompile_InitOnly_SSR` (projet vide compile),
> `NoHandler_SkipsHTTP` (vérifie routes_gen.go), tous les compile tests.
>
> **roadmaps migré** : step.go → step_gen.go + step.go ; app.go → `routes_gen.go` +
> `registerGeneratedRoutes(r)`. Dry-run confirme : seul le `_gen` est régénéré,
> app.go/step.go/step.templ intouchés.
>
> Seuls les markers **schema.sql** restent (volontaire : pas de split en SQL).

### (Référence) plan d'origine

**Problème.** Les markers sont fragiles (drift au reformat) et mélangent généré +
écrit-main dans un même fichier (`domain/*.go`, `app.go`).

**Fix.**
- **Domain** : split `{model}_gen.go` (struct + GetID/WithID, régénéré) +
  `{model}.go` write-once (`Validate()` + méthodes). → plus de markers domain.
  Remplace `patchDomainMarkers`. *(modèle ent.)*
- **Routes** : générer `routes_gen.go` avec `RegisterGeneratedRoutes(r, handlers)` ;
  `app.go` write-once l'appelle (+ routes custom). → plus de bloc marker dans `app.go`.
- `schema.sql` : garder les markers (SQL n'a pas de split), **ou** passer à un
  fichier schéma par model.

Résultat : **zéro marker, zéro édition de généré**, modèle 100 % cohérent.

**Touche** : domain (gen+write-once), `writeRoutes` → `routes_gen.go`, templates.
**Effort** : élevé. **Impact** : élevé (cohérence). → **objectif de fond.**

## P5 — Robustesse & DX — ✅ FAIT

- ✅ **En-têtes de contrat** : générés = `// Code generated by scaffold. DO NOT EDIT.` ;
  write-once user = `// SAFE TO EDIT — scaffold writes this file once and never
  regenerates it.` (service/store/handler/domain). Les 2 buckets sont visuellement nets.
- ✅ **Warning marker manquant** : **caduc** — P4b a supprimé le patching de marqueur.
- ✅ **`--diff`** : `scaffold gen … --diff` affiche un **diff unifié** (via
  `go-difflib`) de chaque fichier qui changerait, **sans rien écrire** (implique
  dry-run). Helper `recordDiff` branché dans tous les writers ; `Result.Diffs` +
  `Result.Print`. Test : `TestScaffold_Diff`. Validé sur roadmaps.
- ⬜ **Connu (non bloquant)** : les `.dylib` sqlite_ext du boilerplate sont
  **macOS-only** (`.dylib` ≠ `.so` Linux). Sujet de portabilité à part (livrer des
  extensions par OS ou les rendre optionnelles) — hors périmètre du polish.

---

## À NE PAS casser (les points forts actuels)

- Le split **`_gen.go` / write-once** (`writeGoFile` vs `writeGoFileOnce`).
- Le **dry-run** via `Result{Created/Overwritten/Unchanged}` (`Result.Print` :105) — transparence excellente.
- **Merge de champs** (gen ré-exécuté fusionne, `--remove` retire, jamais de drop silencieux).
- **Migrations diff** (`writeAlterMigration` :687).

## Ordre conseillé

0. **P0** ✅ (collision de migrations) — bug bloquant, corrigé + testé.
1. **P1** ✅ (seam custom App-level) — fait + testé sur tous les modes.
2. **P3** ✅ (views write-once + `--regen-views`) — supprime la peur de perte.
3. **P2** ✅ (ops granulaires `--skip`/`--only`) — fait + testé sur tous les modes.
4. **P4** ✅ markers retirés (domaine split + routes_gen.go) ; seul schema.sql garde des markers (volontaire).
5. **P5** ✅ en-têtes de contrat + `--diff` (reste : portabilité `.dylib`, connu/hors polish).

---

## Future Improvements & Edge Cases (June 2026)

### 1. Database Integrity & Constraint Safety

#### 1.1 Cross-Reference Constraints on Model Deletion (`scaffold destroy`)
* **Problem:** Destroying a model (e.g. `scaffold destroy ModelA`) drops its table. If another model `ModelB` has a foreign key referencing it (e.g. `fk=model_a`), the `DROP TABLE` migration will fail at execution time on databases with active foreign keys.
* **Fix:** In `Generator.Destroy(model)`, scan the manifest of models for any field containing `fk=<target_table>`. If referencing fields are found, either block the operation or issue a warning listing the dependent tables and fields.

#### 1.2 Validation of Foreign Key Targets during Generation (`scaffold gen`)
* **Problem:** `scaffold gen Post user_id:string,fk=users` only checks if `users` is a valid SQL identifier, not if it exists as a model in the project manifest.
* **Fix:** Inspect the manifest to verify the target table exists, and output a warning if it is missing:
  `Warning: Foreign key target table "users" is not present in the manifest of scaffolded models. Make sure this table exists in your database.`

#### 1.3 SQLite Foreign Key Enforcement at Runtime
* **Problem:** Although the default DSN includes `_foreign_keys=on`, SQLite disables foreign keys by default at the connection level. If a developer uses a custom DSN or runs tests without this query parameter, SQLite silently ignores all foreign key constraints.
* **Fix:** Explicitly enforce foreign keys programmatically inside `sqlite/store.go.tmpl`:
  ```go
  _, err = db.Exec("PRAGMA foreign_keys = ON;")
  ```

### 2. Developer Experience & Safeguards

#### 2.1 Destruction Backup System (`scaffold destroy` Safety Net)
* **Problem:** `scaffold destroy` deletes the user-owned custom files (e.g., `{model}_service.go`, `{model}_store.go`, `{model}_handler.go`, `*.templ`) which contain hand-written business logic, with only a simple confirmation prompt.
* **Fix:** 
  1. Add a `--keep-custom` flag to only delete generated (`_gen.go`) files and write migrations.
  2. Implement an automatic backup: copy the user-owned files to `.scaffold/backups/<timestamp>/` before deleting them.

#### 2.2 SQLite WAL Mode & Concurrency Tuning
* **Problem:** `sqlite/store.go.tmpl` hardcodes `SetMaxOpenConns(1)` to avoid database locking, which serializes all concurrent operations.
* **Fix:** Since WAL mode is enabled by default, SQLite supports concurrent readers. By programmatically executing `PRAGMA journal_mode=WAL;` and setting `PRAGMA busy_timeout=5000;` on startup, the pool can safely allow multiple connections (e.g. `SetMaxOpenConns(10)`), allowing concurrent reads to speed up SSR/REST pages under concurrent traffic.

### 3. Advanced Production-Ready Features

#### 3.1 Multi-Column Constraints (Indexes and Uniques)
* **Problem:** Real-world schemas often require composite constraints (e.g., a unique constraint on `(project_id, slug)` or a compound index on `(category_id, created_at)`). The current syntax only allows single-column modifiers.
* **Fix:** Add flag support for composite constraints during `scaffold gen`, such as `--unique-together project_id,slug`.

#### 3.2 Soft Deletes
* **Problem:** Directly deleting rows with `DELETE` is dangerous or discouraged in production. Soft deletion (setting a `deleted_at` timestamp) is preferred.
* **Fix:** Introduce a `--soft-delete` flag to `scaffold gen`. When enabled, it:
  * Adds a `deleted_at` nullable timestamp column.
  * Updates the generated `Delete()` store method to run an `UPDATE` instead of `DELETE`.
  * Appends `WHERE deleted_at IS NULL` to all generated queries (`Get` and `List`).

### 4. Code Quality & Tooling Inconsistencies

#### 4.1 Import Formatting and Lint Compliance
* **Problem:** Formatting uses `go/format` (`gofmt`), which groups all imports (standard library, third-party, local module) into a single block and sorts them alphabetically. This causes linting failures (like `goimports` or `gci`).
* **Fix:** Use `golang.org/x/tools/imports` in `formatter.GoSource` to format imports according to idiomatic Go grouping rules.

