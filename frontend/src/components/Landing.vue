<script setup lang="ts">
defineProps<{ loginUrl: string }>()
</script>

<template>
  <section class="card landing">
    <h2>Мемкоины MemeAlerts за баллы канала Twitch</h2>
    <p>
      Сервис создаёт на вашем Twitch-канале награду за баллы канала «Получить мемкоины». Зритель тратит баллы и
      получает мемкоины в вашем MemeAlerts. Если его нет среди ваших саппортеров, баллы автоматически возвращаются.
    </p>

    <ol class="how">
      <li>Войдите через Twitch.</li>
      <li>Подключите свой аккаунт MemeAlerts.</li>
      <li>Задайте цену награды, число мемкоинов и лимиты. Награда появится на канале.</li>
    </ol>

    <div class="safe">
      <b>Это безопасно</b>
      <ul>
        <li>
          Twitch запрашивает один доступ: управление наградами за баллы канала (<code>channel:manage:redemptions</code>).
          Сервис не получает ваш пароль и почту, не видит чат и не может писать в него или менять стрим.
        </li>
        <li>Доступ к MemeAlerts запрашивается отдельно: список саппортеров и начисление мемкоинов, ничего больше.</li>
        <li>
          Токены хранятся в зашифрованном виде. MemeAlerts отключается кнопкой в панели (токены при этом удаляются),
          доступ Twitch можно отозвать в его настройках.
        </li>
        <li>
          Исходный код открыт:
          <a href="https://github.com/compico/tw-srv" target="_blank" rel="noopener">github.com/compico/tw-srv</a>.
        </li>
      </ul>
    </div>

    <a class="btn primary" :href="loginUrl">Войти через Twitch</a>
  </section>
</template>
