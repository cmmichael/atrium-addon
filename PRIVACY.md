# Domiel privacy policy

Last updated 15 November 2026.

Domiel is an iPhone and iPad app, with a Home Assistant add-on, for controlling your own home. It is published by
Constantinos Michael. This policy covers the app and the add-on.

## The short version

Domiel has no accounts and no servers that keep your data. The app talks to your own Home Assistant, at home or
over your own Tailscale network. The publisher collects nothing about you, your home or how you use the app. There
is no advertising, no analytics service and no tracking.

## What stays between the app and your Home Assistant

- **Your home.** Rooms, devices, their states, camera images, energy readings, music and everything else the app
  shows comes from your Home Assistant and goes back to it when you change something. It is kept on your device
  only while the app shows it.
- **Sign-in.** Tailscale identifies you to the add-on. The app keeps your home's address and a session key in your
  device's keychain.
- **Diagnostics.** The app keeps a short log of its own behaviour (launches, connection times, errors) on the device.
  Unless you turn off "Send diagnostics to the add-on" in Settings, it sends that log to your own add-on, which
  writes it to your Home Assistant's log. It holds nothing about the home: no device, state or name.

## What leaves your home, and only when you use it

- **AI Assist.** A question you type or speak goes to your Home Assistant's Assist. If you have set Assist up to use
  a cloud AI service, Home Assistant sends the question to that service under its terms. Domiel does not choose or
  contact that service itself.
- **Voice.** When you ask by voice, iOS turns your speech into text on the device where it can. Where it cannot,
  Apple's speech recognition handles it under Apple's privacy policy. The microphone is used only while you ask.
- **Notifications.** If you allow notifications and your add-on sends an alert, Apple's push service carries it to
  your device. Your add-on either sends it to Apple directly, with its own key, or through the publisher's push
  relay. The relay sees the device token and the alert's title and body on the way to Apple. It keeps none of it and
  logs no content.
- **Updates.** The app and the add-on check GitHub for newer versions of Domiel. GitHub sees that request like any
  web request.
- **Tailscale.** If you connect through Tailscale, Tailscale's own app and service handle that connection under
  Tailscale's privacy policy.

## Permissions

- **Camera:** only to scan the code your Home Assistant shows, so you need not type its address.
- **Face ID:** only to confirm before a door is unlocked or the alarm is changed. Apple keeps your face data.
- **Local network:** to find your Home Assistant at home.
- **Microphone and speech recognition:** only while you ask AI Assist by voice.

## Children

Domiel is a tool for a household's own home and does not knowingly collect anything from anyone, children
included.

## Your choices

You can delete the app at any time, which removes everything it kept on the device. Uninstalling the add-on removes
what it kept in your Home Assistant. Since the publisher holds no data about you, there is nothing to ask for or
delete beyond that.

## Contact

Questions about this policy: open an issue at https://github.com/cmmichael/atrium-addon/issues.

Changes to this policy are published at this address, with the date above.
