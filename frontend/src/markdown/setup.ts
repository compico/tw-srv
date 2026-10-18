// Общая настройка md-editor-v3. Подгружается только вместе с редактором и просмотром markdown.
//
// Всё нужное лежит в бандле: по умолчанию библиотека подтягивает mermaid, katex, highlight.js,
// prettier, cropper и screenfull с unpkg.com. Здесь тяжёлые модули выключены, а cropper (обрезка картинки
// перед загрузкой) и screenfull подключены локально.
import Cropper from 'cropperjs'
import 'cropperjs/dist/cropper.min.css'
import DOMPurify from 'dompurify'
import { config } from 'md-editor-v3'
import screenfull from 'screenfull'
import 'md-editor-v3/lib/style.css'

// Из markdown допускаются только картинки, загруженные на наш сервер: внешние картинки
// выдавали бы адрес читателя (например, администратора) стороннему сайту.
const UPLOAD_SRC = /^\/api\/uploads\/[0-9a-f]{32}$/

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node instanceof HTMLImageElement) {
    if (!UPLOAD_SRC.test(node.getAttribute('src') ?? '')) node.remove()
  } else if (node instanceof HTMLAnchorElement && node.hasAttribute('href')) {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer nofollow')
  }
})

// Вызывается библиотекой над готовым HTML. Сырой HTML в markdown и так выключен, это второй слой защиты.
export function sanitize(html: string): string {
  return DOMPurify.sanitize(html, {
    FORBID_TAGS: ['style', 'form', 'input', 'button', 'iframe', 'object', 'embed', 'svg', 'math', 'link', 'meta'],
    FORBID_ATTR: ['style'],
  })
}

config({
  editorExtensions: { cropper: { instance: Cropper }, screenfull: { instance: screenfull } },
  editorConfig: {
    languageUserDefined: {
      'ru-RU': {
        toolbarTips: {
          bold: 'Жирный',
          underline: 'Подчёркнутый',
          italic: 'Курсив',
          strikeThrough: 'Зачёркнутый',
          title: 'Заголовок',
          sub: 'Нижний индекс',
          sup: 'Верхний индекс',
          quote: 'Цитата',
          unorderedList: 'Маркированный список',
          orderedList: 'Нумерованный список',
          task: 'Список задач',
          codeRow: 'Код в строке',
          code: 'Блок кода',
          link: 'Ссылка',
          image: 'Изображение',
          table: 'Таблица',
          revoke: 'Отменить',
          next: 'Повторить',
          preview: 'Предпросмотр',
          previewOnly: 'Только просмотр',
        },
        titleItem: { h1: 'Заголовок 1', h2: 'Заголовок 2', h3: 'Заголовок 3', h4: 'Заголовок 4', h5: 'Заголовок 5', h6: 'Заголовок 6' },
        imgTitleItem: { link: 'Добавить ссылку', upload: 'Загрузить файл', clip2upload: 'Обрезать и загрузить' },
        linkModalTips: {
          linkTitle: 'Добавить ссылку',
          imageTitle: 'Добавить изображение',
          descLabel: 'Описание:',
          descLabelPlaceHolder: 'Введите описание',
          urlLabel: 'Адрес:',
          urlLabelPlaceHolder: 'Введите адрес',
          buttonOK: 'Готово',
        },
        clipModalTips: { title: 'Обрезка изображения', buttonUpload: 'Загрузить' },
        copyCode: { text: 'Копировать', successTips: 'Скопировано', failTips: 'Не удалось скопировать' },
        footer: { markdownTotal: 'Символов', scrollAuto: 'Синхронная прокрутка' },
      },
    },
  },
})

// Свойства, одинаковые для редактора и просмотра: без внешних модулей и CDN.
export const commonProps = {
  language: 'ru-RU',
  sanitize,
  noMermaid: true,
  noKatex: true,
  noEcharts: true,
  noHighlight: true,
  noPrettier: true,
} as const
