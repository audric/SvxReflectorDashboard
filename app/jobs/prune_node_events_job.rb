class PruneNodeEventsJob < ApplicationJob
  queue_as :default

  RETENTION_DAYS = 2
  BATCH_SIZE = 20_000

  def perform(*event_types)
    cutoff = RETENTION_DAYS.days.ago
    scope  = NodeEvent.where(event_type: event_types).where("created_at < ?", cutoff)

    total = 0
    start = Time.now
    loop do
      ids = scope.limit(BATCH_SIZE).pluck(:id)
      break if ids.empty?
      total += NodeEvent.where(id: ids).delete_all
    end

    Rails.logger.info(
      "[PruneNodeEventsJob] event_types=#{event_types.inspect} " \
      "deleted=#{total} cutoff=#{cutoff.iso8601} elapsed=#{(Time.now - start).round(1)}s"
    )
  end
end
