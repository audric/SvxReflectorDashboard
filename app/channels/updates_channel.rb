class UpdatesChannel < ApplicationCable::Channel
  def subscribed
    stream_from 'updates'
  end
end
