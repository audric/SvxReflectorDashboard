#!/usr/bin/env bash
set -e

# If no Gemfile, bootstrap rails app
if [ ! -f /app/Gemfile ]; then
  echo "Bootstrapping Rails app..."
  /usr/local/bin/bootstrap_app.sh
fi

cd /app

# Run migrations (safe if DB not configured)
if [ -f bin/rails ]; then
  bundle check || bundle install --jobs 4
fi

echo "Starting Rails server..."
exec bundle exec rails server -b 0.0.0.0 -p 3000
