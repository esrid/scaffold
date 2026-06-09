# Revue du boilerplate & edge cases — scaffold

> Notes accumulées en utilisant `scaffold` sur le projet **roadmaps** (SSR + SQLite).
> Sépare ce qui est **corrigé** (avec le commit/fichier) de ce qui **reste à revoir**.
> Date de la dernière passe : 2026-06-09.

---

## 0. Passe « production-ready » du 2026-06-09 (revue complète)

Corrigés cette passe (tous testés, suite complète verte, vérifiés en bootant
une app SSR et une app REST générées) :

**Migrations qui échouaient à l'exécution (P0)**
- `ALTER TABLE ADD COLUMN … NOT NULL` sans DEFAULT → toujours rejeté par SQLite
  (et par Postgres sur table non vide). Les colonnes NOT NULL ajoutées reçoivent
  un `DEFAULT` zéro (`''`/`0`/`FALSE`/`CURRENT_TIMESTAMP`/`'[]'`/`'{}'`) —
  `prepareAlterFields` dans `context.go`.
- `unique` sur colonne ajoutée → SQLite refuse l'inline `UNIQUE` dans ADD COLUMN ;
  remplacé par `CREATE UNIQUE INDEX`. `index` sur colonne ajoutée ne créait
  jamais l'index dans la migration (seulement schema.sql) — corrigé, et les
  index sont DROP avant DROP COLUMN (SQLite refuse de dropper une colonne indexée).
- Tests : `TestScaffold_AddNotNullColumn_MigrationHasDefault`,
  `TestScaffold_AddIndexedColumn_MigrationCreatesIndex`.

**SQL généré**
- `List` sans `ORDER BY` → pagination non déterministe. Désormais
  `ORDER BY id DESC` (UUIDv7 = tri chronologique, index PK). Les deux stores.
- Store Postgres : pgx remonte les violations de contrainte à `CollectOneRow`,
  pas à `Query` → les doublons UNIQUE devenaient des 500 au lieu de 409.
  Check `IsUniqueViolation` déplacé dans la bonne branche (Create + Update).
- `default=` : littéraux intelligemment quotés (nombres/TRUE/FALSE/NULL/CURRENT_*
  bruts, le reste quoté avec échappement `''` → `default=O'Brien` ne casse plus).

**Validation des noms (cassaient le Go ou le SQL générés)**
- Mots réservés SQL (`order`, `user`, `to`, `group`, `desc`…) rejetés avec
  message clair (le SQL généré ne quote jamais les identifiants).
- Variantes des champs auto-gérés (`Id`, `ID`, `createdAt`, `Updated_At`…)
  rejetées (collision struct Go / colonne SQLite case-insensitive).
- Cible `fk=` et `--table-name` validées comme identifiants SQL.
- `check=` : split des modifiers paren-aware → `check=status IN ('a','b')` passe.

**Drift silencieux**
- Re-gen avec un type/modifier changé sur un champ existant ne créait AUCUNE
  migration (struct + schema.sql mis à jour, DB non) → warning CLI explicite
  (`Model.ChangedFields`, message « write a manual migration »).

**Générateur**
- `writeRegistry` ne triait pas les modèles → `registry.go` non déterministe à
  chaque run (ordre de map). Trié ; idempotence verrouillée par test.
- `writeProto`/`writeGRPCHandler`/`writeSSRHandler` testaient `fileExists` APRÈS
  écriture → nouveaux fichiers rapportés "Overwritten". `writeSQLFile` rapportait
  toujours "Created". `Destroy` rapportait les vues SSR fantômes. Corrigés.
- `ManifestEntry` perdait `ScaffoldedAt` à chaque gen. Préservé.
- `SaveManifest` atomique (temp + rename).
- `replaceSchemaBlock` tolère un schema.sql absent (projets legacy).

**Boilerplate / handlers**
- SSR `bindForm` ignorait les champs `time`/`json` (aucune branche) → ajoutés
  (parse multi-layout + `json.Valid`) ; vues : input `datetime-local` pour time.
- SSR `serverError` : `AlreadyExistsError` → 409 (était 500).
- REST `List` ignorait `?limit=`/`?offset=` (hardcodé 10,0) → parsés, clampés
  (défaut 20, max 100).
- `display()` : `json.RawMessage` s'affichait comme bytes (`[123 34…]`) → texte ;
  slices jointes par `", "`.
- Proto : import `struct.proto` inutile (et dupliqué si 2 champs json) supprimé.

**CLI**
- `scaffold init` dans un dossier non vide écrasait silencieusement → refus
  (dotfiles type `.git` tolérés).
- Aides `gen`/`destroy` mises à jour (markers/app.go/chemin proto obsolètes).

**Régressions verrouillées (demandées en 1.2/1.3/3.3)**
- `TestDestroy_MigrationVersionAvoidsDiskCollision` (anti-collision destroy)
- `TestScaffold_AllNoHandler_RegistryHasNoUnusedImports` (REST + SSR)
- `TestDestroy_LastHandlerModel_RegistryDropsHTTPImport`
- `TestScaffold_Registry_DeterministicOrder` (invariant idempotence)
- + `validation_test.go` côté parser (mots réservés, collisions, fk=, check=,
  ScaffoldedAt, ChangedFields, version disk-aware).

Restent ouverts (inchangés) : 2.1 (backup avant destroy), 3.1 (goimports),
3.2 (FK croisées au destroy), portabilité `.dylib`, colonnes UNIQUE inline
non-droppables sur SQLite (limite moteur).

---

## 1. Edge cases rencontrés et CORRIGÉS

### 1.1 Collision de versions de migration — `gen` (P0, corrigé avant)
- **Symptôme** : `nextMigrationVersion` ne se basait que sur le compteur du manifest,
  produisant des numéros déjà présents sur le disque (migrations écrites à la main,
  schéma initial du boilerplate, etc.).
- **Fix** : scan du disque au chargement → `highestMigrationOnDisk()` +
  `Manifest.migrationFloor`, pris en compte dans `nextMigrationVersion()`.
- **Fichiers** : `internal/parser/manifest.go`, `internal/parser/model.go`.

### 1.2 Collision de versions de migration — `destroy` (corrigé cette passe)
- **Symptôme** : `scaffold destroy Step` a généré `00007_drop_steps.sql` alors que
  `00007_create_users.sql` existait déjà → **deux migrations `00007`** (goose casse).
- **Cause** : `destroy` passait par `ModelFromEntry`, qui calculait la version avec
  `entry.MigrationVersion + 1` — il **contournait** le fix disk-aware du 1.1.
- **Fix** : `ModelFromEntry(name, entry, manifest)` utilise maintenant
  `nextMigrationVersion(manifest)`.
- **Fichiers** : `internal/parser/model.go`, `cmd/scaffold/destroy.go`.
- **⚠️ Régression à verrouiller** : ajouter un test
  `destroy` qui crée un modèle, ajoute des migrations non liées sur le disque, puis
  détruit et vérifie que la DROP migration ne collisionne pas.

### 1.3 Import `httpadapter` inutilisé dans `registry.go` (corrigé cette passe)
- **Symptôme** : un projet composé **uniquement** de modèles `--no-handler`
  (ou laissé sans handler après un `destroy` du dernier modèle à handler) générait
  un `registry.go` qui importe `httpadapter` **sans l'utiliser** → `go build` échoue
  (`imported and not used`). Idem `domain` côté templates REST.
- **Cause** : l'import était gardé par `{{- if .Models}}` alors qu'il n'est utilisé
  que si **au moins un** modèle a un handler.
- **Fix** : nouveau champ `registryCtx.HasHandlers` (calculé dans `writeRegistry`),
  imports gardés par `{{- if .HasHandlers}}` (et `{{- if and .GRPC .HasHandlers}}`
  pour `grpcadapter`). Appliqué aux 4 templates de registry.
- **Fichiers** : `internal/generator/context.go`, `internal/generator/generator.go`,
  `internal/generator/templates.go` (`registryTmpl`, `registryTmplPostgres`,
  `registryTmplSSR`, `registryTmplSSRPostgres`).
- **⚠️ Régression à verrouiller** : test « projet de modèles tous `--no-handler` →
  `registry.go` ne contient pas `httpadapter` et compile ».

### 1.4 Extension SQLite `sqlean` macOS-only cassait la prod Ubuntu (corrigé avant)
- **Symptôme** : `.dylib` chargé en local (macOS) absent en prod Linux → `uuid7()`
  (et autres fonctions sqlean) indisponibles.
- **Fix** : `sqleanPath()` durci (env `SQLITE_EXT_PATH` → relatif à l'exe → relatif au
  repo) + Dockerfile qui télécharge le bundle `sqlean-linux-x86`.
- **Note** : le bundle `sqlean` contient TOUTES les extensions (crypto/fuzzy/regexp/…),
  pas seulement la génération d'ID.
- **Côté boilerplate** : `internal/adapters/store/store.go` (généré une fois,
  extensible). À garder en tête si le boilerplate fournit un `store.go` par défaut.

### 1.5 Fichiers `.go` dans le boilerplate sont compilés (corrigé avant)
- **Symptôme** : un template terminé en `.go` (ex. `custom.go`) est vu comme du code
  du module scaffold lui-même et est compilé / casse `go test ./...`.
- **Fix** : tout template de fichier source doit finir en **`.go.tmpl`**.
- **Règle** : aucun `.go` « template » dans `internal/generator/boilerplate/**`.

---

## 2. Comportements à CONNAÎTRE (pas des bugs, mais piégeux)

### 2.1 `destroy` supprime le code utilisateur
`{model}_service.go` et `{model}_store.go` (et `_handler.go`, `.templ`) contiennent
la **logique écrite à la main** — `destroy` les supprime après un simple prompt `y/N`.
→ Documenté dans l'aide de la commande, mais **aucun backup automatique**.
Recommandation : option `--keep-custom` ou déplacement vers `*.bak` avant suppression.

### 2.2 Faux positifs gopls (« undefined: domain.X », « Stores.X undefined »)
Récurrents après regen : gopls ne réindexe pas immédiatement les `_gen.go`.
**`go build ./...` est la source de vérité**, pas les diagnostics LSP. Pas un bug
scaffold — mais à mentionner dans le README pour éviter la panique.

### 2.3 Merge de champs au re-`gen`
`scaffold gen Model field:type` **fusionne** : un champ existant est mis à jour en
place, un nouveau est ajouté, les non-mentionnés sont conservés. Pour retirer :
`--remove`. `noHandler` et la sélection d'ops (`--skip/--only`) sont **préservés**
depuis le manifest (`NoHandler = passé || existant`, `OpsFromSkipped`). Bien.

### 2.4 Méthodes de store custom et ordre des colonnes
Quand on ajoute une colonne, les `_gen.go` (dont `scan{Model}Rows`) sont régénérés
avec le **nouvel ordre de colonnes**. Toute requête SQL **écrite à la main** dans
`{model}_store.go` qui réutilise `scan{Model}Rows` doit lister les colonnes dans le
**même ordre**, sinon le `Scan` est décalé. (Vécu en ajoutant `rationale` à
`CanonicalStep` → `ListByRoadmap` custom à mettre à jour.)
→ Piste : exposer une **constante de liste de colonnes** générée
(`{model}AllColumns`) réutilisable par le code custom, pour ne pas dupliquer l'ordre.

---

## 3. À REVOIR sur le boilerplate (ouvert)

### 3.1 Groupement des imports (gofmt vs goimports)
Les `registry.go` générés produisent parfois un **seul groupe** d'imports trié
alphabétiquement (stdlib + modules mélangés). C'est **gofmt-clean** mais pas
idiomatique (goimports sépare stdlib / tiers par une ligne vide).
→ Décider : lancer `goimports` (ou un regroupement) dans `writeGoFile`, ou laisser tel quel.

### 3.2 `destroy` : nettoyage transverse complet ?
Vérifier que `destroy` retire bien **toutes** les traces :
- routes (`routes_gen.go`) ✅ (régénéré depuis le manifest)
- registry ✅
- migrations : crée un DROP, mais **ne supprime pas** les anciennes `create/alter`
  du modèle (normal — historique goose). OK, mais à documenter.
- références **croisées** : si un autre modèle a une FK vers le modèle détruit,
  rien ne prévient → la DROP migration peut échouer à l'exécution. **À couvrir.**

### 3.3 Couverture de tests des chemins `destroy` et `--no-handler`
Les deux bugs de cette passe (1.2 et 1.3) n'étaient **pas** couverts par les tests.
→ Ajouter des tests générateur :
- destroy avec migrations disque non liées (anti-collision)
- projet 100 % `--no-handler` → registry compile, pas d'import mort
- destroy du **dernier** modèle à handler → registry repasse sans `httpadapter`

### 3.4 Génération concurrente / idempotence
Un `scaffold gen Model` sans nouveaux champs régénère les `_gen.go` **sans** créer de
migration (vérifié). Bon comportement d'idempotence — à garder comme invariant testé.

### 3.5 `store.go` / `sqlean` dans le boilerplate par défaut
S'assurer que le `store.go` boilerplate (hardening pool WAL + `sqleanPath`) est bien
**write-once** et non écrasé, et que le Dockerfile fourni par `init` télécharge
l'extension Linux. (Spécifique à SQLite + sqlean.)

---

## 4. Invariants à protéger par des tests (synthèse)
1. Aucune **collision de version** de migration, quel que soit le chemin (`gen`, `destroy`).
2. `registry.go` / `routes_gen.go` **compilent toujours**, y compris 0 handler.
3. Re-`gen` sans champ = **idempotent**, pas de migration parasite.
4. `noHandler` et la sélection d'ops **survivent** au re-`gen`.
5. Aucun fichier généré n'est **non-gofmt** (idéalement non-goimports non plus).
6. `destroy` ne touche que les fichiers du modèle ciblé.
