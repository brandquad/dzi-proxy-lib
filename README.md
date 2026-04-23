# dzi-proxy-lib

Go-библиотека для отдачи тайлов Deep Zoom Image из ZIP-архивов в S3 и для сборки диапазона тайлов в одно изображение.

## Что делает

- принимает DZI URL с hex-encoded именем ZIP
- скачивает ZIP из S3 при первом запросе
- распаковывает архив в локальный cache directory
- отдаёт отдельные тайлы из локального кеша
- умеет собирать диапазон тайлов в одно JPEG-изображение через отдельный `/composite/...` endpoint

## Как устроен архив

Ожидается ZIP-архив с DZI-структурой уровней:

```text
archive.zip
  page_1_files/
    0/
      0_0.webp
    1/
      0_0.webp
      1_0.webp
```

При распаковке `.dzi` и `.xml` файлы пропускаются. Тайлы сохраняются в локальный кеш по пути:

```text
{CacheDir}/{md5(zip_key)}/{level}/{col}_{row}.{ext}
```

## Конфиг

```go
type Config struct {
    Listen            string
    S3AccessKey       string
    S3SecretKey       string
    S3Region          string
    S3Bucket          string
    S3Host            string
    S3UseSSL          bool
    CleanupTimeoutCfg int
    CleanupTimeout    time.Duration
    CacheDir          string
    HttpCacheDays     int
    Silent            bool
    Debug             bool
}
```

Минимально нужно задать:

- `Listen`
- `S3AccessKey`
- `S3SecretKey`
- `S3Region`
- `S3Bucket`
- `S3Host`
- `CacheDir`
- `HttpCacheDays`
- `Debug`

## Запуск

```go
cfg := &dziproxylib.Config{
    Listen:         ":8080",
    S3AccessKey:    "...",
    S3SecretKey:    "...",
    S3Region:       "us-east-1",
    S3Bucket:       "my-bucket",
    S3Host:         "https://s3.example.com",
    S3UseSSL:       true,
    CacheDir:       "./cache",
    HttpCacheDays:  7,
    CleanupTimeout: time.Hour,
}

srv, err := dziproxylib.DziProxyServer(cfg)
if err != nil {
    panic(err)
}

go dziproxylib.CleanupCache()
panic(srv.ListenAndServe())
```

## Основной endpoint

Сервер принимает исходный DZI-путь и отдаёт конкретный тайл:

```text
/18/dzi/page_1/{hex_zip_name}/12/34_56.jpg
```

или:

```text
/18/dzi_bw/page_1/{hex_zip_name}/12/34_56.webp
```

Где `{hex_zip_name}` это hex-encoded строка вида:

```text
98d0393e-4b23-4884-b38f-9b55d4a1e906(Yellow).zip
```

После декодирования сервер использует этот путь как S3 key.

## Composite endpoint

Для сборки диапазона тайлов используется отдельный route:

```text
/composite/18/dzi_bw/page_1/{hex_zip_name}?level={int}&col_min={int}&col_max={int}&row_min={int}&row_max={int}&overlap={int}&max_size={int}&is_color={bool}
```

### Обязательные query-параметры

- `col_min`
- `col_max`
- `row_min`
- `row_max`

### Опциональные query-параметры

- `level`
  если не передан, берётся максимальный доступный уровень из распакованного архива
- `overlap`
  по умолчанию `1`
- `max_size`
  ограничение на большую сторону итогового изображения с сохранением пропорций
- `is_color`
  по умолчанию `true`
  если `false`, итоговый буфер собирается как `image.Gray`

### Поведение

- итог всегда отдаётся как `image/jpeg`
- overlap учитывается при склейке соседних тайлов
- если задан `max_size`, изображение собирается сразу в уменьшённый размер
- для `webp` тайлов используется декодер `golang.org/x/image/webp`

### Пример

```text
/composite/18/dzi_bw/page_1/39386430333933652d346232332d343838342d623338662d3962353564346131653930362859656c6c6f77292e7a6970?col_min=10&col_max=20&row_min=5&row_max=12&max_size=2000&is_color=false
```

## Кеш и конкурентность

- ZIP скачивается и распаковывается один раз на `key`
- параллельные запросы к одному и тому же архиву синхронизируются через mutex + wait group
- максимальный `level` для `/composite/...` кешируется в памяти по `key`
- фоновая очистка удаляет старые директории из кеша по `CleanupTimeout`

## Memory trace

Для `/composite/...` включены memory trace логи:

- `composite.after_prepare_archive`
- `composite.before_build`
- `composite.after_build`
- `composite.after_encode`

Логи выводятся только если `Config.Debug == true`.

Функция логирования находится в [memstats.go](./memstats.go).

## Ограничения

- composite endpoint возвращает только JPEG
- обычный tile endpoint сейчас выставляет `Content-Type: image/jpeg`, даже если файл тайла имеет другое расширение
- библиотека хранит распакованные архивы на локальном диске
