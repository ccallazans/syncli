# Syncli

`syncli` é uma ferramenta de linha de comando, escrita em Go, para sincronizar
um diretório entre máquinas na **mesma rede local**.

Cada máquina roda o `syncli` apontando para um diretório e um nome de *grupo*.
Os nós do mesmo grupo se descobrem automaticamente via mDNS (zeroconf, sem
servidor central), e as alterações feitas em um diretório são propagadas para os
demais nós do grupo.

## Objetivo

Oferecer uma forma simples de manter um diretório sincronizado entre
computadores de uma rede local, sem depender de serviço na nuvem nem de
configuração de servidor. É um projeto de estudo/experimentação, não um produto
pronto para produção.

### Como funciona, em resumo

- **Descoberta:** os nós se anunciam e se encontram por mDNS no serviço
  `_syncli._tcp` (compatível com Avahi/Bonjour). Apenas nós do mesmo grupo se
  conectam entre si.
- **Observação de arquivos:** cada nó observa seu diretório recursivamente e
  detecta mudanças (com debounce).
- **Transferência:** as mudanças são enviadas via TCP aos peers do grupo. Para
  arquivos grandes, apenas os blocos alterados são transferidos (delta de 64 KB);
  arquivos pequenos são enviados por inteiro. Ao entrar em um grupo que já
  existe, o nó pede um *snapshot* completo a um peer para começar com uma cópia
  íntegra dos arquivos.

### Limitações (sem rodeios)

- Funciona apenas em **rede local**.
- A comunicação **não é criptografada**. Use somente em redes confiáveis.
- A resolução de conflitos é **last-write-wins** (a última escrita vence).

## Requisitos

- Go 1.24+ (para compilar)
- Uma rede local com mDNS/multicast habilitado

## Instalação

Compilar a partir do código:

```bash
git clone https://github.com/ccallazans/syncli.git
cd syncli
go build -o syncli ./cmd/syncli
```

Ou instalar diretamente com o Go:

```bash
go install github.com/ccallazans/syncli/cmd/syncli@latest
```

### Docker

O projeto inclui um `Dockerfile`:

```bash
docker build -t syncli .
docker run --rm --network host syncli --group fotos --dir /data
```

> O mDNS depende de multicast na rede, por isso normalmente é necessário
> `--network host`.

## Comandos

```
syncli --group <nome> --dir <caminho> [opções]   # inicia a sincronização
syncli peers [--timeout <duração>]               # lista os peers na rede
syncli help                                       # mostra a ajuda
```

### Opções de sincronização

| Opção | Descrição | Padrão |
|-------|-----------|--------|
| `--group <nome>` | Nome do grupo de sincronização (obrigatório) | — |
| `--dir <caminho>` | Diretório local a sincronizar (obrigatório) | — |
| `--port <porta>` | Porta TCP de escuta | `9001` |
| `--peer <host:porta>` | Conecta manualmente a um peer (pode repetir) | — |
| `--auto-join` | Entra no grupo sem pedir confirmação | `false` |
| `--verbose` | Logs de depuração | `false` |

### Opções do comando `peers`

| Opção | Descrição | Padrão |
|-------|-----------|--------|
| `--timeout <duração>` | Tempo de varredura da rede | `5s` |

### Variáveis de ambiente

| Variável | Descrição |
|----------|-----------|
| `SYNCLI_NODE_ID` | Define um ID fixo para o nó (por padrão é `hostname-pid`) |
| `SYNCLI_EXP_LOG` | Caminho de um arquivo JSONL para registro de eventos |

## Exemplos

Compartilhar um diretório (o primeiro nó cria o grupo):

```bash
syncli --group fotos --dir ~/Fotos
```

Entrar em um grupo já existente (faz a varredura e pede confirmação):

```bash
syncli --group fotos --dir ~/fotos-local
```

O mesmo, mas sem o prompt de confirmação (útil em scripts/Docker):

```bash
syncli --group fotos --dir ~/fotos-local --auto-join
```

Rodar dois grupos na mesma máquina (use portas diferentes):

```bash
syncli --group fotos      --dir ~/Fotos      --port 9001
syncli --group documentos --dir ~/Documentos --port 9002
```

Listar os peers e seus grupos na rede:

```bash
syncli peers
syncli peers --timeout 10s
```

Adicionar um peer manualmente quando o mDNS não estiver disponível:

```bash
syncli --group fotos --dir ~/Fotos --peer 192.168.1.10:9001
```

Para encerrar um nó em execução, use `Ctrl+C`.

## Licença

Distribuído sob os termos do arquivo [LICENSE](LICENSE).
