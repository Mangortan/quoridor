import os
import json

# Сканируем проект на наличие файлов Go, модулей и веба
files_to_include = []
for root, dirs, files in os.walk('.'):
    if any(p in root for p in ['.git', 'downloads', '__pycache__', '.vscode']):
        continue
    for file in files:
        if file.endswith(('.go', '.mod', '.html')):
            full_path = os.path.join(root, file)
            rel_path = os.path.relpath(full_path, '.')
            files_to_include.append(rel_path)

files_dict = {}
for fpath in files_to_include:
    try:
        with open(fpath, 'r', encoding='utf-8') as rf:
            files_dict[fpath] = rf.read()
    except Exception as e:
        print(f"Пропуск файла {fpath}: {e}")

cells = []

# Клетка 1: Запись всех файлов проекта в окружение блокнота Modal
write_files_source = ["# Автоматическое создание структуры проекта Quoridor\n", "import os\n\n"]
for path, content in files_dict.items():
    dir_name = os.path.dirname(path)
    if dir_name:
        write_files_source.append(f"os.makedirs({repr(dir_name)}, exist_ok=True)\n")
    write_files_source.append(f"with open({repr(path)}, 'w', encoding='utf-8') as f:\n")
    write_files_source.append(f"    f.write({repr(content)})\n\n")

cells.append({
    "cell_type": "code",
    "execution_count": None,
    "metadata": {},
    "outputs": [],
    "source": write_files_source
})

# Клетка 2: Определение Modal App, образа с Go и удаленной функции на 16 ядер
modal_code = [
    "import modal\n",
    "import os\n",
    "\n",
    "# Инициализация приложения Modal\n",
    "app = modal.App(\"quoridor-spsa-tune-notebook\")\n",
    "\n",
    "# Создаем легковесный контейнер Debian и устанавливаем Go 1.22\n",
    "go_image = (\n",
    "    modal.Image.debian_slim()\n",
    "    .apt_install(\n",
    "        \"wget\",\n",
    "        \"tar\"\n",
    "    )\n",
    "    .run_commands(\n",
    "        \"wget -q https://go.dev/dl/go1.22.4.linux-amd64.tar.gz\",\n",
    "        \"tar -C /usr/local -xzf go1.22.4.linux-amd64.tar.gz\",\n",
    "        \"rm go1.22.4.linux-amd64.tar.gz\",\n",
    "    )\n",
    "    .env({\"PATH\": \"/usr/local/go/bin:\" + os.environ.get(\"PATH\", \"\")})\n",
    ")\n",
    "\n",
    "# Выделяем мощный инстанс на 16 CPU и 8 ГБ RAM\n",
    "@app.function(image=go_image, cpu=16.0, memory=8192, timeout=7200)\n",
    "def run_tune_remote(iters: int = 1000, pairs: int = 30, depth: int = 3, workers: int = 16):\n",
    "    import subprocess\n",
    "    print(\"=== Проверка тестов Go ===\")\n",
    "    subprocess.run([\"go\", \"test\", \"./...\"])\n",
    "    \n",
    "    print(f\"\\n=== Запуск SPSA тюнинга на {workers} ядрах в облаке Modal ===\")\n",
    "    cmd = f\"go run ./cmd/tune -iters {iters} -pairs {pairs} -depth {depth} -workers {workers}\"\n",
    "    subprocess.run(cmd, shell=True)\n"
]

cells.append({
    "cell_type": "code",
    "execution_count": None,
    "metadata": {},
    "outputs": [],
    "source": modal_code
})

# Клетка 3: Запуск удаленной задачи прямо из блокнота
cells.append({
    "cell_type": "code",
    "execution_count": None,
    "metadata": {},
    "outputs": [],
    "source": [
        "# Запуск вычислений на мощном облачном сервере Modal\n",
        "with app.run():\n",
        "    run_tune_remote.remote(iters=1000, pairs=30, depth=3, workers=16)\n"
    ]
})

notebook = {
    "cells": cells,
    "metadata": {
        "language_info": { "name": "python" }
    },
    "nbformat": 4,
    "nbformat_minor": 2
}

with open("quoridor_modal.ipynb", "w", encoding="utf-8") as f:
    json.dump(notebook, f, ensure_ascii=False, indent=2)

print("Готово! Создан файл quoridor_modal.ipynb")

