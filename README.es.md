# AegisCodeAgent

[![CI](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml)
[![Aegis Code Review](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml)
![Go 1.23+](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md)

AegisCodeAgent es un agente de revisión de código nativo de Go que se ejecuta automáticamente en los Pull Requests de GitHub. Combina análisis determinista, contexto a nivel de repositorio, razonamiento con LLM y verificación independiente para que solo los hallazgos respaldados por evidencias lleguen a la revisión final.

> **Hito actual: v0.6.** Aegis ya puede utilizarse como revisor nativo del repositorio mediante GitHub Actions y como CLI local. Actualmente se centra en Go, utiliza DeepSeek como primer proveedor de razonamiento y avanza hacia la evaluación cuantitativa y el endurecimiento para producción.

## ¿Qué ocurre al abrir un Pull Request?

No es necesario iniciar un servidor ni mantener un proceso local en ejecución. GitHub Actions inicia Aegis, revisa el Head exacto del PR con respecto a su Base y publica el resultado en el Pull Request.

```text
Pull Request abierto o actualizado
          │
          ▼
 orquestación mediante un Workflow de confianza
          │
          ▼
 Git Diff y alcance de líneas modificadas
          │
          ├──────────────► analizadores deterministas de Go
          │                 go test · go vet
          │
          └──────────────► motor de contexto del repositorio
                            AST · tipos · llamadores · pruebas
                                      │
                                      ▼
                         agente de razonamiento DeepSeek
                                      │
                                      ▼
                           verificador de evidencias
                                      │
                                      ▼
                 Annotations P0-P3 · Job Summary · informe HTML
```

El modelo no decide directamente si el código puede fusionarse. Los hallazgos estáticos ya incluyen evidencias reproducibles. Los candidatos generados por el modelo deben superar comprobaciones de ubicación, Diff, instantánea del código fuente y diagnóstico focalizado antes de convertirse en hallazgos finales. Las hipótesis rechazadas o no concluyentes se conservan para auditoría, pero no se publican como hallazgos finales.

## Arquitectura

El sistema se divide en etapas con entradas y salidas explícitas:

| Etapa | Responsabilidad | Salida | Implementación |
| --- | --- | --- | --- |
| Orquestación de GitHub | Responder a eventos del PR, obtener la Base de confianza y el Head exacto, cancelar ejecuciones obsoletas | Entorno de revisión reproducible | `.github/workflows/aegis-review.yml` |
| Motor de Diff | Resolver revisiones de forma segura y analizar Diffs de tres puntos, cambios de nombre, binarios, Hunks y líneas modificadas | Change Set normalizado | `internal/gitdiff/` |
| Análisis estático | Ejecutar analizadores en paralelo con Timeout, normalizar diagnósticos y eliminar duplicados | Hallazgos respaldados por evidencias | `internal/analyzer/` |
| Motor de contexto | Indexar declaraciones y relaciones de tipos de Go, y priorizar símbolos modificados y relacionados dentro de un presupuesto | Repository Context Bundle | `internal/context/` |
| Reasoning Agent | Permitir que DeepSeek inspeccione evidencias acotadas mediante herramientas de solo lectura y proponga candidatos estructurados | Candidatos sin verificar | `internal/agent/` |
| Verifier | Validar la identidad y ubicación de los candidatos, repetir comprobaciones focalizadas y correlacionar evidencias independientes | Veredictos Verified/Rejected/Inconclusive | `internal/verifier/` |
| Publisher | Convertir severidades a P0-P3, aplicar el umbral de fusión y generar las salidas de GitHub y los informes completos | Summary, Annotations, HTML/JSON | `internal/githubreport/`, `internal/report/` |
| Límite de credenciales | Eliminar variables con forma de credencial de los subprocesos controlados por el repositorio | Entorno de subprocesos saneado | `internal/secureenv/` |

### Ciclo de vida de un Finding

```text
diagnóstico determinista ─────────────────────────────► Finding final

candidato del modelo
      │
      ▼
validación de Schema + límite del repositorio + línea modificada
      │
      ▼
correlación con evidencias focalizadas de Test/Vet
      │
      ├── Verified ───────────────────────────────────► Finding final
      ├── Rejected ───────────────────────────────────► solo registro de auditoría
      └── Inconclusive ───────────────────────────────► solo registro de auditoría
```

El informe final utiliza un modelo de dominio `ReviewReport` estable que comparten la CLI, el Publisher de GitHub, el Renderer HTML y la interfaz de automatización JSON. De este modo, la lógica de revisión se mantiene independiente de la presentación.

## Inicio rápido: revisión automática en GitHub

Esta es la forma recomendada de utilizar Aegis en este repositorio o en un Fork propio.

### 1. Preparar el repositorio

Si es necesario, crea un Fork del repositorio y clónalo:

```bash
git clone https://github.com/<YOUR_GITHUB_NAME>/AegisCodeAgent.git
cd AegisCodeAgent
go test ./...
```

Comprueba que GitHub Actions esté habilitado en **Settings → Actions → General**.

### 2. Elegir el modo de revisión

El modo determinista no requiere ningún Secret:

```text
go test + go vet + repository context + GitHub report
```

Para activar el flujo completo de razonamiento y verificación, crea un Repository Secret en **Settings → Secrets and variables → Actions**:

```text
Name:  DEEPSEEK_API_KEY
Value: <your DeepSeek API key>
```

Los Pull Requests procedentes de Forks y Dependabot nunca reciben este Secret y utilizan automáticamente el modo determinista.

### 3. Enviar una rama de funcionalidad normal

```bash
git switch master
git pull --ff-only origin master
git switch -c feature/my-change

# Edita código o documentación.
git add .
git commit -m "feat: describe the change"
git push -u origin feature/my-change
```

Abre un Pull Request de `feature/my-change` hacia `master`. El evento `opened` inicia Aegis; cada Push posterior emite un evento `synchronize`, inicia una revisión nueva y cancela la ejecución obsoleta.

Los Pull Requests que solo modifican documentación también activan el Workflow y reciben un Summary y un Artifact con el informe. Cuando la comparación no contiene cambios de código fuente compatibles, Aegis omite el razonamiento de DeepSeek y el Verifier. Así evita llamadas innecesarias al modelo sin debilitar las comprobaciones deterministas.

### 4. Consultar el resultado

Abre el Pull Request y revisa:

1. **Conversation** para consultar el comentario de revisión que Aegis mantiene actualizado y el enlace al informe HTML completo.
2. **Checks → Aegis Code Review** para ver el estado de ejecución y el Job Summary.
3. **Annotations** para consultar los hallazgos asociados a archivos y líneas modificados.
4. **Artifacts → aegis-review-report** para descargar `review.html` y `review.json`.
5. La conclusión final del Check para conocer la decisión de fusión.

| Prioridad | Significado | Annotation de GitHub | Bloquea por defecto |
| --- | --- | --- | :---: |
| P0 | Critical | Error | Sí |
| P1 | High | Error | Sí |
| P2 | Medium | Warning | No |
| P3 | Low / Info | Notice | No |

Para aplicar el resultado de forma obligatoria, añade `Aegis Code Review` como Required Status Check en el Branch Ruleset de `master`. La configuración de notificaciones de GitHub se encarga de los avisos web y por correo electrónico; Aegis no ejecuta un servicio de correo separado.

> Actualmente, Aegis es un Workflow nativo del repositorio y no una Action de GitHub Marketplace. Funciona de inmediato en este repositorio y en sus Forks. Para instalarlo en un repositorio no relacionado todavía es necesario incorporar tanto el código fuente de Aegis como su Workflow; empaquetarlo como Action reutilizable es trabajo futuro.

## Inicio rápido: CLI local

Requisitos: Go 1.23 o posterior y Git.

### Revisión determinista sin API Key

```bash
go build -o aegis ./cmd/aegis

./aegis review \
  --repo . \
  --base master \
  --head HEAD \
  --output review.html
```

Abre `review.html` en un navegador. El Worktree debe estar limpio y coincidir con `HEAD`, ya que los analizadores operan sobre el sistema de archivos real.

### Revisión completa con DeepSeek + Verifier

```bash
cp .aegis.example.json .aegis.json
cp .env.example .env

# Guarda la clave real únicamente en el archivo .env ignorado por Git.
./aegis review \
  --config .aegis.json \
  --repo . \
  --base master \
  --head HEAD \
  --output review.html
```

`.aegis.json` almacena la configuración no sensible del Provider, los presupuestos y los Timeout. La API Key solo se lee desde `DEEPSEEK_API_KEY` o desde el archivo `.env` ignorado. El Endpoint oficial de DeepSeek es obligatorio, salvo que se permita explícitamente un Endpoint personalizado.

Comandos útiles:

```bash
# Informe legible por máquinas
./aegis review --repo . --base master --head HEAD --format json --output review.json

# Ejecutar todos los Adapter cuando staticcheck y gosec estén instalados
./aegis review --repo . --base master --head HEAD --analyzers all --output review.html

# Consultar todas las opciones disponibles
./aegis review --help
./aegis github --help
```

## Modelo de seguridad

- El Review Binary se compila desde el Commit Base de confianza del PR, mientras que el Commit Head exacto se obtiene en un directorio separado como objetivo del análisis.
- El Workflow utiliza permisos de repositorio de solo lectura y desactiva la persistencia de credenciales del Checkout.
- Los PR de Forks y Dependabot no reciben `DEEPSEEK_API_KEY` ni Tokens con permisos de escritura.
- Git, Test, Vet, Staticcheck y Gosec se ejecutan sin variables de entorno con forma de credencial.
- Las herramientas del modelo son de solo lectura y están limitadas por ruta del repositorio, intervalo de líneas y volumen de salida.
- GitHub considera las ramas del mismo repositorio como fuentes de confianza con acceso a Secrets. Restringe los permisos de escritura y exige revisión para los cambios en `.github/workflows/`.

## Guía de desarrollo

Límites de los Packages:

```text
cmd/aegis/             CLI orchestration and user-facing errors
internal/gitdiff/      revision resolution and unified-diff parsing
internal/analyzer/     analyzer adapters, scheduling, normalization
internal/context/      AST/type index, relationships, ranking, budgets
internal/config/       strict JSON config and narrow dotenv loading
internal/agent/        provider protocol, prompt, tools, reasoning loop
internal/verifier/     candidate validation and evidence adjudication
internal/githubreport/ P0-P3 mapping, Summary, annotations, merge gate
internal/secureenv/    child-process credential isolation
internal/review/       shared domain model
internal/report/       self-contained HTML, JSON, and Markdown renderers
```

Ejecuta los controles de calidad antes de abrir un Pull Request:

```bash
go fmt ./...
go vet ./...
go test -race ./...
go build ./cmd/aegis
```

Cada nueva fuente de Findings debe proporcionar una ubicación real, severidad, categoría, origen, confianza y evidencia reproducible. Los nuevos Candidates del Agent deben permanecer separados de los Findings finales hasta que termine la verificación.

## Alcance actual y hoja de ruta

Completado:

- Pipeline determinista de analizadores de Go.
- Motor de contexto del repositorio.
- Bucle de razonamiento DeepSeek acotado.
- Verificación independiente de Candidates.
- Informe HTML de evidencias autocontenido.
- Trigger de GitHub Actions, Annotations, Artifact y Merge Gate.

Siguiente:

- Corpus de evaluación seleccionado con PR que contienen Bugs y PR limpios.
- Medición de Precision, Recall, False Positives, Latency y Cost.
- Empaquetado como GitHub Action reutilizable y distribución de Releases.
- Proveedores de modelos adicionales y Observability para producción.

## Licencia

[MIT](LICENSE)
