import { APP_VERSION } from "./version";

export type ReleaseNote = {
  version: string;
  date: string;
  title: Record<"ru" | "en", string>;
  items: Record<"ru" | "en", string[]>;
};

export const RELEASE_NOTES_STORAGE_KEY = "telegram-companion.seen-release-version";

export const RELEASE_NOTES: ReleaseNote[] = [
  {
    version: "0.8.6",
    date: "7 сентября 2026",
    title: { ru: "Импорт TData и исправления подключения", en: "TData import and connection fixes" },
    items: {
      ru: [
        "В разделе «Аккаунты» можно добавить TData аккаунты из публичных ZIP-файлов и папок Google Drive: одна ссылка на строку, до 100 ссылок.",
        "Исправлено выполнение вступлений в группы по заданному интервалу.",
        "Исправлены проверка лицензии при запуске и запуск публичной версии с пустым профилем.",
        "Отсутствие новых обновлений больше не отображается как ошибка.",
        "Встроенная конфигурация Tor обновлена; резервный Lyrebird также обновлён.",
        "Подключение не гарантируется у каждого провайдера."
      ],
      en: [
        "Import TData accounts from public Google Drive ZIP files and folders in Accounts: one link per line, up to 100 links.",
        "Group joins now follow the configured interval.",
        "Fixed startup license checks and public builds starting with an empty profile.",
        "Finding no new updates no longer displays an error.",
        "Embedded Tor configuration and the Lyrebird fallback were refreshed.",
        "Connectivity is not guaranteed with every ISP."
      ]
    }
  },
  {
    version: "0.8.5",
    date: "30 августа 2026",
    title: { ru: "Устойчивые прокси и понятная диагностика", en: "Resilient proxies and clear diagnostics" },
    items: {
      ru: [
        "Встроенный транспорт Lyrebird с obfs4 использует зафиксированную проверенную конфигурацию и работает как основной обход блокировок.",
        "Snowflake сохранён как резервный транспорт, если obfs4 временно недоступен.",
        "Ошибки активации, проверки лицензии и обновления теперь показываются понятно, без раскрытия чувствительных данных.",
        "Отзыв и замена лицензий сохраняют fail-closed поведение; локальные profile, tdata и Keychain не перезаписываются при обновлении."
      ],
      en: [
        "The bundled Lyrebird transport with obfs4 uses a pinned reviewed configuration as the primary censorship-circumvention route.",
        "Snowflake remains available as a fallback transport when obfs4 is temporarily unavailable.",
        "Activation, license-check, and update failures now show clear diagnostics without exposing sensitive data.",
        "License revocation and replacement retain fail-closed behavior; local profile, tdata, and Keychain state is not overwritten by an update."
      ]
    }
  },
  {
    version: "0.8.3",
    date: "25 августа 2026",
    title: { ru: "Безопасность лицензий и обновлений", en: "License and update security" },
    items: {
      ru: [
        "Приложение проверяет подписанный список отзывов при запуске и во время работы: если лицензия отозвана или обязательная проверка недоступна, новые операции безопасно блокируются.",
        "Поставка обновлений получила дополнительные проверки целостности, неизменности проверенного кандидата и безопасного отката.",
        "Удалён удалённый перенос новых tdata-учётных записей. Локальные tdata-аккаунты, сохранённые сессии и локальный импорт остаются доступны."
      ],
      en: [
        "The app checks a signed revocation list at startup and while running; new operations are safely blocked when a license is revoked or a required check is unavailable.",
        "Delivery of updates now includes additional integrity checks, verified-candidate immutability, and safe rollback controls.",
        "Remote transfer of newly supplied tdata credentials has been removed. Local tdata accounts, saved sessions, and local import remain available."
      ]
    }
  },
  {
    version: "0.8.2",
    date: "31 июля 2026",
    title: { ru: "Минус-слова и управление интервалами", en: "Negative keywords and interval controls" },
    items: {
      ru: [
        "В разделе «Ключевые слова» появился подраздел «Минус-слова»: отдельные слова и словосочетания из него полностью исключают срабатывание триггеров.",
        "Keyword-триггеры поддерживают операторы Яндекс Директа: фиксацию словоформы и служебного слова, точное количество слов, порядок слов, группы альтернатив и минус-слова.",
        "Интервал вступления аккаунтов можно включать и выключать; доступный диапазон расширен от 0 до 3000 минут.",
        "При выключении интервала сохранённые отложенные вступления очищаются, а новые вступления больше не получают случайную задержку.",
        "При выключенной отлёжке аккаунты удаляются из списка отлёжки, таймеры очищаются и ответы на разрешённые триггеры доступны сразу.",
        "Состояния интервала и отлёжки защищены от повторного появления при параллельном сохранении и после перезапуска приложения.",
        "Загрузка сохранённых настроек стала безопаснее: временное пустое состояние интерфейса больше не может перезаписать keyword, интервалы и отлёжку.",
        "В таблице каналов появились отдельные действия «Вступить» и «Выйти»: они управляют участием всех подходящих Telegram-аккаунтов, не удаляя канал из списка.",
        "Переключатель «Активен» теперь управляет только внутренней обработкой канала: ответами по keyword для ботов-спамеров или сбором аналитики для разведчиков.",
        "Тематика обязательна при добавлении каналов; незаполненное поле теперь сразу подсвечивается красным и показывает понятную ошибку.",
        "\u0412 \u0440\u0430\u0437\u0434\u0435\u043b\u0435 \u00ab\u041c\u043e\u0434\u0435\u0440\u0430\u0446\u0438\u044f \u043a\u0430\u043d\u0430\u043b\u043e\u0432\u00bb \u0434\u043b\u044f \u0441\u0442\u0430\u0442\u0443\u0441\u0430 \u00ab\u041e\u0436\u0438\u0434\u0430\u0435\u0442 \u043e\u0434\u043e\u0431\u0440\u0435\u043d\u0438\u044f\u00bb \u0434\u043e\u0431\u0430\u0432\u043b\u0435\u043d\u0430 \u043a\u043d\u043e\u043f\u043a\u0430 \u00ab\u0412\u0441\u0442\u0443\u043f\u0438\u0442\u044c \u0437\u0430\u043d\u043e\u0432\u043e\u00bb: \u043e\u043d\u0430 \u043f\u043e\u0432\u0442\u043e\u0440\u043d\u043e \u0441\u0442\u0430\u0432\u0438\u0442 \u043e\u0436\u0438\u0434\u0430\u044e\u0449\u0438\u0435 \u0430\u043a\u043a\u0430\u0443\u043d\u0442\u044b \u0432 \u043e\u0447\u0435\u0440\u0435\u0434\u044c \u0432\u0441\u0442\u0443\u043f\u043b\u0435\u043d\u0438\u044f \u0438 \u043e\u0431\u043d\u043e\u0432\u043b\u044f\u0435\u0442 \u0432\u0440\u0435\u043c\u044f \u043d\u043e\u0432\u043e\u0439 \u0437\u0430\u044f\u0432\u043a\u0438 \u043f\u043e\u0441\u043b\u0435 \u043e\u0442\u0432\u0435\u0442\u0430 Telegram."
      ],
      en: [
        "The Keywords section now includes Negative keywords: any configured word or phrase globally suppresses trigger matching.",
        "Keyword triggers support Yandex Direct-style operators for exact word forms and service words, exact word counts, word order, alternative groups, and exclusions.",
        "The account join interval can be enabled or disabled, with an expanded range from 0 to 3000 minutes.",
        "Disabling the interval clears scheduled join delays and prevents new joins from receiving a random delay.",
        "When account rest is disabled, accounts disappear from the rest list, timers are cleared, and permitted trigger replies become available immediately.",
        "Interval and rest state cannot be recreated by concurrent persistence or after an application restart.",
        "Saved settings now hydrate safely, preventing temporary empty UI state from overwriting keywords, intervals, or account rest.",
        "The channel table now has separate Join and Leave actions that manage all eligible Telegram accounts without deleting the channel.",
        "The Active switch now controls only internal channel processing: keyword replies for spammer accounts or analytics collection for scouts.",
        "A topic is required when adding channels; an empty field is now highlighted in red with a clear validation message.",
        "Channel Moderation now shows Join again for Awaiting approval rows, requeueing only pending accounts and refreshing the request time after Telegram responds."
      ]
    }
  },
  {
    version: "0.8.1",
    date: "29 июля 2026",
    title: { ru: "Горячие ответы и импорт триггеров", en: "Hot replies and trigger import" },
    items: {
      ru: [
        "В разделе «Ключевые слова» появилась кнопка «Добавить» для массового импорта позитивных канонов из текста и файлов TXT.",
        "Формы в скобках привязываются к канону, а импортированный позитивный канон сразу включается как keyword-триггер.",
        "Изменение единого ответа в комментариях или ЛС применяется без остановки Telegram-listener: подключённые аккаунты продолжают реагировать на триггеры.",
        "Сообщения всех Telegram-аккаунтов, подключённых к приложению, исключены из trigger-обработки: боты больше не отвечают друг другу."
      ],
      en: [
        "The Keywords section now provides an Add action for bulk importing positive canons from text and TXT files.",
        "Forms in parentheses are attached to their canon, and an imported positive canon is immediately enabled as a keyword trigger.",
        "Changing the shared comment or direct-message reply no longer stops the Telegram listener, so connected accounts keep reacting to triggers.",
        "Messages authored by any Telegram account connected to the application are excluded from trigger processing, preventing bot-to-bot reply loops."
      ]
    }
  },
  {
    version: "0.8.0",
    date: "26 июля 2026",
    title: { ru: "Отлёжка аккаунтов и безопасные каналы", en: "Account rest and safe channel management" },
    items: {
      ru: [
        "Добавлен раздел «Отлёжка аккаунтов»: для каждого вступления видны канал, статус, начало, окончание и оставшееся время.",
        "После вступления публичные ответы учитывают заданную отлёжку; её можно временно включать и выключать, не сбрасывая таймеры.",
        "Составные keyword с несколькими словами срабатывают, когда все слова встречаются в одном сообщении, даже если между ними есть другие слова.",
        "В Live-статистике выделенный фрагмент исходного сообщения можно перенести в негативные keyword для последующей модерации.",
        "При удалении канала приложение сначала выводит из него подключённые аккаунты и показывает состояние удаления до завершения.",
        "Удаление канала получило понятную подпись, а состояние выхода аккаунтов остаётся доступным до полного завершения операции.",
        "Очереди вступления и состояния участников устойчиво восстанавливаются после перезапуска приложения."
      ],
      en: [
        "The Account rest section shows the channel, status, start, end, and remaining time for every join.",
        "Public replies respect the configured rest period after joining; it can be toggled without resetting timers.",
        "Compound keywords match when all configured words occur in the same message, even with words between them.",
        "In Live statistics, a selected source-message fragment can be moved to negative keywords for moderation.",
        "Deleting a channel first removes connected accounts and keeps its removal status visible until completion.",
        "Channel deletion now has a clear text action while account-leave progress remains visible until completion.",
        "Join queues and membership states are restored reliably after the application restarts."
      ]
    }
  },
  {
    version: "0.7.0",
    date: "21 июля 2026",
    title: { ru: "Публичная версия для Apple Silicon", en: "Public Apple Silicon release" },
    items: {
      ru: [
        "Очередь вступления в каналы работает независимо от keyword-триггеров и продолжает обработку при остановленных ответах.",
        "После запуска приложение возобновляет ранее запланированные вступления с сохранёнными интервалами и статусами.",
        "Live-статистику можно выгрузить в CSV за выбранный диапазон дат без ограничения текущей страницей.",
        "Выгрузка повторяет текущие сортировку, порядок и видимость столбцов таблицы.",
        "Добавлена публичная сборка для Mac с Apple Silicon и macOS 13 или новее; до активации Telegram-службы и локальная база не запускаются.",
        "Лицензии выпускаются офлайн отдельным генератором для Windows и привязываются к Machine ID конкретного Mac.",
        "Раздел «Обновления» поддерживает автоматическую и ручную проверку, загрузку и установку подписанных выпусков через Sparkle.",
        "Публичный GitHub-репозиторий используется только для release-файлов, appcast и кратких заметок о выпуске."
      ],
      en: [
        "The channel join queue runs independently from keyword triggers and continues while replies are stopped.",
        "Previously scheduled joins resume after launch with their saved intervals and statuses.",
        "Live statistics can be exported to CSV for a selected date range without current-page limits.",
        "Exports follow the table's current sorting, column order, and visibility.",
        "A public build now targets Apple Silicon Macs running macOS 13 or newer; Telegram services and the local database remain stopped before activation.",
        "Licenses are issued offline by a separate Windows generator and bound to the Machine ID of one Mac.",
        "The Updates section supports automatic and manual checks, downloads, and installation of signed releases through Sparkle.",
        "The public GitHub repository is used only for release files, the appcast, and concise release notes."
      ]
    }
  },
  {
    version: "0.6.0",
    date: "18 июля 2026",
    title: { ru: "Модерация каналов и планирование ЛС", en: "Channel moderation and scheduled direct messages" },
    items: {
      ru: [
        "В панели «Модерация каналов» показаны статусы вступления и заявок по каналам и аккаунтам.",
        "Вступления и заявки выполняются последовательно с настраиваемым интервалом.",
        "В «Сообщениях в ЛС» можно планировать личные сообщения для 1–3 контактов, выбирать аккаунты и задавать ограниченное число повторов.",
        "В списке доступны все аккаунты-спамеры с поиском; недоступные помечены соответствующим статусом.",
        "При открытии приложения автоматизация остановлена и запускается вручную для безопасности."
      ],
      en: [
        "Channel moderation dashboard shows join and application statuses for each channel and account.",
        "Channel joins and applications run sequentially at a configurable interval.",
        "Schedule direct messages for 1–3 contacts, select accounts, and set a finite number of repeats.",
        "All spammer accounts are visible and searchable; unavailable accounts are marked accordingly.",
        "Automation opens stopped and requires a manual start for safety."
      ]
    }
  },
  {
    version: "0.5.3",
    date: "17 июля 2026",
    title: { ru: "Управляемое вступление в каналы", en: "Managed channel activation" },
    items: {
      ru: [
        "Новые каналы добавляются неактивными и не запускают вступление до явного включения.",
        "Перед активацией приложение подтверждает добавление всех аккаунтов и создаёт отдельное состояние вступления для каждого из них.",
        "В списке каналов видны суммарные состояния участников: вступили, ожидают модерации, вступают или завершились ошибкой."
      ],
      en: [
        "New channels are added inactive and do not start joining until explicitly enabled.",
        "Activation confirms adding all accounts and creates an individual membership state for each account.",
        "The channel list summarizes members, pending approvals, active joins, and failed attempts."
      ]
    }
  },
  {
    version: "0.5.2",
    date: "17 июля 2026",
    title: { ru: "Раздельные тексты сообщений", en: "Separate reply messages" },
    items: {
      ru: [
        "Для комментариев и личных сообщений доступны два независимых единых ответа.",
        "Изменённый текст применяется к следующим отправкам сразу, без перезапуска автоматизации.",
        "Статистика и планирование используют единый календарь приложения с выбором даты и времени."
      ],
      en: [
        "Public replies and direct messages have independent shared reply texts.",
        "Edited text is applied to subsequent deliveries immediately without restarting automation.",
        "Statistics and scheduling use one themed application date-and-time picker."
      ]
    }
  },
  {
    version: "0.5.1",
    date: "16 июля 2026",
    title: { ru: "Live-статистика доставок", en: "Live delivery statistics" },
    items: {
      ru: [
        "Новый раздел хранит обезличенную историю исходных сообщений, сработавших триггеров и результатов доставки.",
        "В таблице показаны тип отправки, дата, время, Telegram-аккаунт и итоговый статус.",
        "Доступны диапазон дат, пагинация, сортировка, изменение порядка, ширины и видимости столбцов.",
        "Счётчики записей и размера базы обновляются вместе с Live-таблицей."
      ],
      en: [
        "A new section stores anonymized source messages, matched triggers, and delivery outcomes.",
        "The table shows delivery type, date, time, Telegram account, and final status.",
        "Date range, pagination, sorting, column reordering, resizing, and visibility controls are available.",
        "Record and database-size counters refresh together with the Live table."
      ]
    }
  },
  {
    version: "0.5.0",
    date: "16 июля 2026",
    title: { ru: "Персональные IP-маршруты", en: "Per-account IP routes" },
    items: {
      ru: [
        "В настройках добавлено управление SOCKS5 и HTTP CONNECT прокси без отображения паролей.",
        "Каждому Telegram-аккаунту можно назначить отдельный IP-маршрут; системный Tor/Snowflake остаётся маршрутом по умолчанию.",
        "Один маршрут обслуживает не более 10 аккаунтов. Недоступные и заполненные маршруты нельзя назначить новым аккаунтам.",
        "Проверка состояния, повторные подключения и пауза после ошибок выполняются отдельно для каждого маршрута.",
        "Смена маршрута перезапускает только затронутый Telegram-аккаунт и не останавливает остальных.",
        "Сохранены чередование личных сообщений и ответов, статистика закрытых личных сообщений и действующая логика keyword-триггеров."
      ],
      en: [
        "Settings now manages SOCKS5 and HTTP CONNECT proxies without exposing passwords.",
        "Each Telegram account can use its own IP route; managed Tor/Snowflake remains the default route.",
        "A route serves at most 10 accounts. Unavailable and full routes cannot be assigned to new accounts.",
        "Health checks, reconnects, and error backoff are isolated per route.",
        "Changing a route restarts only the affected Telegram account.",
        "Private/public delivery alternation, closed-DM statistics, and keyword trigger behavior remain intact."
      ]
    }
  },
  {
    version: "0.4.2",
    date: "16 июля 2026",
    title: { ru: "Чередование ответов и ЛС", en: "Alternating replies and direct messages" },
    items: {
      ru: [
        "Каждый аккаунт по очереди отправляет личное сообщение и ответ в комментариях на подходящие keyword-триггеры.",
        "Если личные сообщения недоступны, аккаунт отвечает в комментариях текстом публичного ответа.",
        "Статистика отдельно считает успешные ЛС, ответы в комментариях и закрытые личные сообщения."
      ],
      en: [
        "Each account alternates direct messages and public replies for matching keyword triggers.",
        "When direct messages are unavailable, the account posts the configured public reply instead.",
        "Statistics separately count delivered DMs, public replies, and closed direct messages."
      ]
    }
  },
  {
    version: "0.4.1",
    date: "15 июля 2026",
    title: { ru: "Управление keyword и статистика", en: "Keyword controls and statistics" },
    items: {
      ru: [
        "Компоновка аналитики стала компактнее, а таблица получила больше рабочего пространства.",
        "Порядок, ширина, видимость, сортировка и размер страницы таблицы сохраняются отдельно для каждой вкладки.",
        "Добавлен массовый импорт позитивных и негативных keyword из текста и файлов TXT.",
        "При импорте запись «канон (форма, форма)» сразу создаёт каноническое слово с дополнительными вариантами.",
        "Улучшена консервативная канонизация русских форм и сленговых вариантов.",
        "Статистика ответов использует реальные сохранённые данные по каждому Telegram-аккаунту."
      ],
      en: [
        "The analytics layout is more compact and gives the table more working space.",
        "Column order, width, visibility, sorting, and page size persist independently for every tab.",
        "Positive and negative keywords can be imported in bulk from text and TXT files.",
        "An imported entry such as ‘canonical (form, form)’ creates a canonical keyword with its variants in one step.",
        "Conservative canonicalization now covers more Russian forms and slang variants.",
        "Reply statistics use real persisted data for each Telegram account."
      ]
    }
  },
  {
    version: "0.4.0",
    date: "15 июля 2026",
    title: { ru: "Тонкая визуальная система", en: "Refined visual system" },
    items: {
      ru: [
        "Интерфейс получил более спокойную структуру с тонкими разделителями и компактными элементами управления.",
        "В настройках теперь можно сразу переключаться между светлой и темной темами; выбранный вариант сохраняется после перезапуска.",
        "Сохранены знакомая геометрия рабочих экранов, масштабирование Ctrl +/- и уважение к системному ограничению анимации.",
        "Разведчики собирают сообщения отдельно от общего START/STOP: доступны интервалы 1, 5, 10, 30, 60 и 120 минут и ручной запуск анализа.",
        "Первое чтение ограничено последними семью днями; дальнейшие запуски продолжаются с сохранённого места для каждого аккаунта и чата.",
        "Keyword распределены по разделам «Все keyword», «Позитивные», «Негативные» и «Служебные» с поиском и сохранением настроек таблиц.",
        "Статистика показывает результат последнего сбора, общие объёмы сообщений, слов, форм, групп и размер базы keyword."
      ],
      en: [
        "The interface now uses a calmer structure with thin separators and compact controls.",
        "Settings now switches immediately between light and dark themes and remembers the selected theme after restart.",
        "Existing workspace geometry, Ctrl +/- scaling, and the system reduced-motion preference are preserved.",
        "Scout collection now has independent START/STOP controls, exact collection intervals, and an Analyze now command.",
        "The first scan is limited to seven days; later scans resume from a persisted cursor for every account and chat.",
        "Keywords are organized into All, Positive, Negative, and Service tabs with search and persistent table preferences.",
        "Live statistics show the latest collection plus total messages, words, forms, groups, and keyword database size."
      ]
    }
  },
  {
    version: "0.3.3",
    date: "15 июля 2026",
    title: { ru: "Служебные слова", en: "Service words" },
    items: {
      ru: [
        "Частые предлоги, союзы и местоимения автоматически отделяются от смысловых keyword.",
        "Вкладка «Служебные» позволяет просматривать, добавлять и удалять русские и английские служебные слова.",
        "Для канонов отображаются язык, формы, сообщения, упоминания и изменение частоты после последнего сбора."
      ],
      en: [
        "Frequent prepositions, conjunctions, and pronouns are separated automatically from meaningful keywords.",
        "The Service tab lists, adds, and removes Russian and English service words.",
        "Canonical rows show language, forms, messages, mentions, and frequency changes since the previous collection."
      ]
    }
  },
  {
    version: "0.3.2",
    date: "15 июля 2026",
    title: { ru: "Планировщик аналитики", en: "Analytics scheduler" },
    items: {
      ru: [
        "Сбор keyword запускается вручную или автоматически с интервалом 1, 5, 10, 30, 60 или 120 минут.",
        "Первое чтение охватывает последние семь дней, а следующие циклы продолжаются с сохранённого места каждого аккаунта и чата.",
        "Панель показывает следующий запуск, объём последнего сбора, число обработанных групп и общие метрики базы."
      ],
      en: [
        "Keyword collection can run manually or every 1, 5, 10, 30, 60, or 120 minutes.",
        "The first scan covers seven days, while later cycles continue from the saved cursor for each account and chat.",
        "The dashboard shows the next run, latest collection volume, processed groups, and all-time database metrics."
      ]
    }
  },
  {
    version: "0.3.1",
    date: "15 июля 2026",
    title: { ru: "Светлая и тёмная темы", en: "Light and dark themes" },
    items: {
      ru: [
        "В настройках доступны светлая и тёмная темы; светлая используется по умолчанию.",
        "Выбранная тема сохраняется и применяется при следующем запуске приложения.",
        "Интерфейс поддерживает масштабирование Ctrl +/- и адаптируется к рабочим разрешениям Ubuntu и macOS."
      ],
      en: [
        "Settings provides light and dark themes, with light selected by default.",
        "The selected theme persists and is restored on the next application launch.",
        "The interface supports Ctrl +/- scaling and adapts to Ubuntu and macOS workspace resolutions."
      ]
    }
  },
  {
    version: "0.3.0",
    date: "14 июля 2026",
    title: { ru: "Каноническая аналитика keyword", en: "Canonical keyword analytics" },
    items: {
      ru: [
        "Аналитика собирает отдельные русские и английские слова и распределяет каноны по разделам «Все keyword», «Позитивные» и «Негативные».",
        "Аккаунтам назначаются роли «Бот-спамер» и «Разведчик»; для разведчиков используется отдельный список каналов сбора.",
        "Все формы канона участвуют в точном срабатывании триггера, а единый ответ применяется работающими ботами без STOP/START.",
        "Ежедневные защищённые резервные копии сохраняют локальную базу, настройки и файлы Telegram-сессий.",
        "Старая история аналитики, keyword, статистика и резервные копии очищаются для нового формата; аккаунты, tdata, роли, каналы, настройки и единый ответ сохраняются.",
        "Новые обезличенные сообщения аналитики хранятся до 100 лет и могут быть очищены вручную."
      ],
      en: [
        "Analytics collects individual Russian and English words and classifies canonical words as neutral, positive, or negative.",
        "Accounts can be assigned Spammer or Scout roles, with a dedicated collection-channel list for scouts.",
        "Every canonical form participates in exact trigger matching, while the shared reply updates running bots without STOP/START.",
        "Protected daily backups preserve the local database, settings, and Telegram session files.",
        "Legacy analytics history, keywords, statistics, and backups are reset for the new format; accounts, tdata, roles, channels, settings, and the shared reply remain.",
        "New anonymized analytics messages are retained for up to 100 years and can be cleared manually."
      ]
    }
  },
  {
    version: "0.2.8",
    date: "14 июля 2026",
    title: { ru: "Чаты комментариев и лимиты Telegram", en: "Comments chats and Telegram limits" },
    items: {
      ru: [
        "Ссылка с #tc-discussion сохраняет и использует именно чат комментариев, а не родительский канал.",
        "FloodWait от Telegram сохраняется с точным временем ожидания, без повторных попыток до окончания лимита."
      ],
      en: [
        "A link with #tc-discussion keeps and uses the comments chat instead of the parent channel.",
        "Telegram FloodWait is saved with its exact expiry and is not retried before the limit expires."
      ]
    }
  },
  {
    version: "0.2.7",
    date: "14 июля 2026",
    title: { ru: "Готовность Snowflake", en: "Snowflake readiness" },
    items: {
      ru: [
        "Telegram запускается после проверенного соединения через локальный Tor/Snowflake SOCKS, даже когда текстовый bootstrap Tor обновляется с задержкой.",
        "Это устраняет ложное состояние ожидания и позволяет аккаунтам подключаться автоматически."
      ],
      en: [
        "Telegram starts after a verified local Tor/Snowflake SOCKS connection, even when Tor text bootstrap lags behind.",
        "This removes the false waiting state and lets accounts connect automatically."
      ]
    }
  },
  {
    version: "0.2.6",
    date: "14 июля 2026",
    title: { ru: "Диагностика подключений", en: "Connection diagnostics" },
    items: {
      ru: [
        "Статусы Telegram-подключений теперь показывают безопасный тип ответа API вместо общего runtime_error.",
        "Исправление направлено на надёжное подключение чатов комментариев для ботов-спамеров."
      ],
      en: [
        "Telegram connection states now show a safe API response type instead of a generic runtime_error.",
        "This update targets reliable comments-chat connections for spammer accounts."
      ]
    }
  },
  {
    version: "0.2.5",
    date: "14 июля 2026",
    title: { ru: "Стабильность подключений", en: "Connection stability" },
    items: {
      ru: [
        "Аккаунты сохраняются в менеджере при промежуточных статусах подключения.",
        "Изменение каналов больше не останавливает подключающиеся аккаунты."
      ],
      en: [
        "Accounts remain managed while they are in transient connection states.",
        "Channel changes no longer stop accounts while they connect."
      ]
    }
  },
  {
    version: "0.2.4",
    date: "14 июля 2026",
    title: { ru: "Импорт discussion-ссылок", en: "Discussion link import" },
    items: {
      ru: [
        "Форма каналов больше не срезает #tc-discussion до передачи в Telegram-слой.",
        "Ссылка на чат комментариев больше не считается дубликатом родительского канала."
      ],
      en: [
        "The channel form no longer strips #tc-discussion before reaching the Telegram layer.",
        "A comments-chat link is no longer treated as a duplicate of the parent channel."
      ]
    }
  },
  {
    version: "0.2.3",
    date: "14 июля 2026",
    title: { ru: "Комментарии каналов", en: "Channel discussions" },
    items: {
      ru: [
        "Ссылки с #tc-discussion сохраняются и направляют ботов в чат комментариев.",
        "Вступление в Telegram выполняется по исходной ссылке, затем проверяется нужный discussion-чат.",
        "Неверные фрагменты ссылок по-прежнему отбрасываются для безопасного импорта."
      ],
      en: [
        "Links with #tc-discussion are preserved and route bots to the comments chat.",
        "Telegram joins through the original link, then verifies the requested discussion chat.",
        "Invalid link fragments continue to be discarded for safe imports."
      ]
    }
  },
  {
    version: "0.2.2",
    date: "14 июля 2026",
    title: { ru: "Рабочие действия и ответы", en: "Working actions and replies" },
    items: {
      ru: [
        "Единый ответ сохраняется до запуска и сразу применяется к работе ботов.",
        "Добавлены мгновенное удаление и массовая очистка ключевых слов и аналитики.",
        "Принятие кандидата аналитики добавляет его в ключевые слова без перезапуска."
      ],
      en: [
        "The shared reply is saved before start and applied to bots immediately.",
        "Added instant deletion and bulk clearing for keywords and analytics.",
        "Accepting an analytics candidate adds it to keywords without a restart."
      ]
    }
  },
  {
    version: "0.2.1",
    date: "14 июля 2026",
    title: { ru: "Стабильность анализа", en: "Analysis stability" },
    items: {
      ru: [
        "Повторный анализ снова формирует список после удаления кандидатов.",
        "Сброс модерации выполняется безопасно после сохранения результата.",
        "Добавлены экран и история изменений приложения."
      ],
      en: [
        "A repeat analysis rebuilds the list after candidate removal.",
        "Moderation resets safely after the result is saved.",
        "Added an update screen and in-app release history."
      ]
    }
  },
  {
    version: "0.2.0",
    date: "14 июля 2026",
    title: { ru: "Первый рабочий выпуск", en: "First working release" },
    items: {
      ru: [
        "Подключение нескольких Telegram-аккаунтов и распределение ответов по очереди.",
        "Каналы, ключевые слова, единый ответ и локальная статистика.",
        "Анонимизированный сбор сообщений разведчиком и анализ ключевых слов."
      ],
      en: [
        "Multiple Telegram accounts with round-robin replies.",
        "Channels, keywords, a shared reply, and local statistics.",
        "Anonymous scout collection and keyword analysis."
      ]
    }
  }
];

type ReleaseNotesStorage = Pick<Storage, "getItem" | "setItem">;

export function releaseNotesForVersion(version: string): ReleaseNote | null {
  return RELEASE_NOTES.find((release) => release.version === version) ?? null;
}

export function currentReleaseNotes(): ReleaseNote {
  return releaseNotesForVersion(APP_VERSION) ?? RELEASE_NOTES[0];
}

export function shouldPresentReleaseNotes(storage: Pick<Storage, "getItem">): boolean {
  return releaseNotesForVersion(APP_VERSION) !== null && storage.getItem(RELEASE_NOTES_STORAGE_KEY) !== APP_VERSION;
}

export function markReleaseNotesSeen(storage: ReleaseNotesStorage): void {
  storage.setItem(RELEASE_NOTES_STORAGE_KEY, APP_VERSION);
}
