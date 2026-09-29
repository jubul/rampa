# Empezar con Rampa en una hora

Esta guía es para quien va a usar Rampa desde la web, sin escribir YAML ni comandos, salvo uno opcional al final. Si preferís la línea de comandos, mirá [`cli.md`](cli.md).

## 0. Qué vas a tener al final

Una cuenta de nube conectada, un cluster de Kubernetes creado por Rampa con el agente ya instalado, y una aplicación desplegada en un scope `dev` con su URL. Después, el mismo circuito sirve para `staging`, `prod` y para entornos efímeros por pull request.

## 1. Entrá y creá tu organización

Entrá con tu cuenta. La primera vez, Rampa te pide crear una organización: un nombre corto (el `slug`, por ejemplo `acme`) y un nombre para mostrar. Todo lo que hagas vive dentro de esa organización, y podés invitar gente con roles `owner`, `admin`, `developer` o `viewer`.

Te va a aparecer la página **Empezar**, con cinco pasos y su estado. Los siguientes puntos son esos pasos.

## 2. Conectá tu cuenta de AWS

En **Empezar → Conectar una cuenta de nube**:

1. Elegí AWS, poné un nombre (por ejemplo `acme-nonprod`) y la región.
2. Rampa te muestra un botón **Crear el rol en AWS**. Abre la consola de CloudFormation con la plantilla ya cargada. Revisá los permisos si querés: son de lectura de ECR y EKS, secretos bajo `rampa/*`, y, si dejás **ManageInfrastructure** en `true`, lo necesario para que Rampa cree clusters y bases de datos por vos.
3. Creá el stack. En un minuto, en la pestaña Outputs, copiá `RoleArn`.
4. Volvé a Rampa, pegá el ARN y tocá **Verificar**. Tiene que quedar en `connected`.

Nunca le das a Rampa una access key. Rampa asume ese rol con un identificador único de tu cuenta, y podés borrar el stack cuando quieras para revocarlo.

Si el botón no aparece y ves un comando en su lugar, es porque tu instalación de Rampa no tiene publicada la plantilla en S3: copiá el comando y corrélo con la CLI de AWS; hace lo mismo.

## 3. Creá un cluster con un click

En **Empezar → Registrar o crear un cluster → Crear cluster con un click**:

1. Elegí la cuenta que conectaste, un nombre (`rampa-nonprod`), el entorno (`nonprod` para dev, staging y pull requests; `prod` solo para producción), la versión de Kubernetes y el dominio que van a usar tus aplicaciones (por ejemplo `apps.dev.acme.com`; después vas a apuntar un registro DNS comodín ahí).
2. Opcional: pegá el ARN de tu usuario o rol de AWS para poder entrar al cluster con `kubectl`.
3. Leé el aviso de costo: el plano de control de EKS y el NAT gateway cuestan alrededor de USD 100 por mes, más los nodos que la carga necesite. Todo queda en tu cuenta de AWS.
4. Tocá **Crear**. Vas a ver el log en vivo. Tarda entre 15 y 25 minutos.

Cuando termina, el cluster aparece **online** en Rampa: el agente ya se instaló solo. No hay nada que copiar ni ejecutar.

**¿Ya tenés un cluster?** Elegí **Registrar cluster existente**, completá nombre, entorno y dominio, y Rampa te da un único comando `helm upgrade --install` para instalar el agente. Ese es el único comando de toda la guía, y solo en este camino.

## 4. Creá tu primera aplicación, sin YAML

En **Empezar → Crear la primera aplicación**, el asistente te lleva por cinco pantallas:

1. **Básicos:** nombre (minúsculas y guiones), equipo, criticidad, tipo (`api` si expone HTTP, `worker` si consume colas, `cron` si corre por horario, `static` si es un sitio) y lenguaje.
2. **Scopes:** dónde va a correr. Dejá `dev` y `prod` marcados, y `pr` si querés un entorno por cada pull request. Elegí tamaño (S, M, L o XL, con la CPU y memoria a la vista), réplicas y estrategia de deploy. Para `prod` podés poner tu dominio propio y autoscaling.
3. **Parameters:** variables de entorno. Un valor plano, un secreto que va a vivir en el gestor de secretos de tu nube, o la credencial de un service.
4. **Services:** la base de datos, la cache o la cola que tu app necesita. Los creás acá y Rampa los provisiona en tu cuenta cuando lleguen a esa fase.
5. **Revisión:** ves el `rampa.yaml` que Rampa generó con lo que elegiste. Podés copiarlo. Tocá **Crear**.

## 5. Bajá el proyecto inicial y hacé el primer push

En la página de la aplicación, tocá **Descargar proyecto inicial**. Es un zip con un servicio mínimo en el lenguaje que elegiste, su `Dockerfile`, el `rampa.yaml` de la app y los workflows de GitHub Actions ya configurados.

1. Descomprimilo y creá un repositorio en GitHub con ese contenido.
2. Push a `main`.
3. El workflow construye la imagen, la sube al registry de tu cuenta y le pide a Rampa el deploy a `dev`.

En un par de minutos, en la página de la app, el scope `dev` pasa a **healthy** y tenés la URL. Cada pull request que abras va a tener su propio entorno con su propia URL, y se destruye al cerrarlo.

## 6. Promové a producción

En la página de la app, en el scope `prod`, tocá **Desplegar**, elegí la imagen (el tag que ya corrió en `dev`) y confirmá. Si tu organización exige aprobación para producción, el deploy queda esperando y otro admin lo aprueba desde la misma pantalla. Vas a ver el avance en vivo: aplicando, progresando, healthy. Si algo falla, el mensaje dice qué pod y por qué.

Volver atrás es desplegar el tag anterior. El historial de deployments está siempre en la página.

## Qué sigue

- Invitá a tu equipo en **Configuración → Miembros**.
- Creá un **token de API** para que otros repos o herramientas hablen con Rampa.
- Mirá [`modelo.md`](modelo.md) para entender el `rampa.yaml` en profundidad y [`cli.md`](cli.md) si querés hacer todo esto desde la terminal.
- Para un cluster de producción separado, repetí el paso 3 con entorno `prod`.
