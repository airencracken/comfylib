#!/bin/sh
app=$1
database="$app.db"
if [ "$app" = tidings ]; then database=reader.db; fi
case "$app" in folkweave|tidings) ;; *) exit 2 ;; esac
apt-get update || exit 1
apt-get install -y adduser ca-certificates init-system-helpers || exit 1
dpkg -i /release.deb || exit 1
[ ! -e "/var/lib/$app/$database" ] || exit 1
[ "$(stat -c %a "/var/lib/$app")" = 700 ] || exit 1
[ "$(stat -c %a "/etc/$app/$app.env")" = 600 ] || exit 1
[ "$(id -u "$app")" != 0 ] || exit 1
"$app" --version || exit 1
case "$app" in
 folkweave)
  printf '%s\n' 'a-package-test-password' | folkweave init --username packageowner --password-stdin || exit 1
  folkweave backup --output /var/lib/folkweave/manual || exit 1
  folkweave restore --from /var/lib/folkweave/manual/folkweave.db --data /var/lib/folkweave/restored || exit 1
  ;;
 tidings)
  install -o tidings -g tidings -m 0600 /dev/null /var/lib/tidings/password || exit 1
  printf '%s\n' 'a-package-test-password' > /var/lib/tidings/password || exit 1
  tidings create-owner --username packageowner --password-file /var/lib/tidings/password || exit 1
  tidings backup --destination /var/lib/tidings/manual || exit 1
  ;;
esac
[ "$(stat -c %U "/var/lib/$app/$database")" = "$app" ] || exit 1
printf '%s\n' '# retained package configuration' >> "/etc/$app/$app.env" || exit 1
dpkg -i /release.deb || exit 1
grep -q '^# retained package configuration$' "/etc/$app/$app.env" || exit 1
dpkg --remove "$app" || exit 1
[ -f "/var/lib/$app/$database" ] || exit 1
dpkg --purge "$app" || exit 1
[ -f "/var/lib/$app/$database" ] || exit 1
getent passwd "$app" || exit 1
printf '%s package lifecycle passed\n' "$app"
