class StatusController < ApplicationController
  skip_forgery_protection

  def show
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://redis:6379/1'))
    nodes       = JSON.parse(redis.get('reflector:snapshot') || '{}')
    trunks      = JSON.parse(redis.get('reflector:trunks') || '{}')
    satellites  = JSON.parse(redis.get('reflector:satellites') || '{}')
    cluster_tgs = JSON.parse(redis.get('reflector:cluster_tgs') || '[]')
    config      = JSON.parse(redis.get('reflector:config') || '{}')

    status = { 'nodes' => nodes, 'trunks' => trunks, 'satellites' => satellites, 'cluster_tgs' => cluster_tgs }
    status.merge!(config)

    render json: status
  end
end
