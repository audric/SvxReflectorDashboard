module Admin
  class NodesController < ApplicationController
    layout false
    before_action :require_admin
    before_action :set_node, only: %i[edit update destroy]

    def index
      @nodes = Node.order(:callsign)
    end

    def new
      @node = Node.new
    end

    def create
      @node = Node.new(node_params)
      if @node.save
        redirect_to admin_nodes_path, notice: "Node #{@node.callsign} created"
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit
    end

    def update
      if @node.update(node_params)
        redirect_to admin_nodes_path, notice: "Node #{@node.callsign} updated"
      else
        render :edit, status: :unprocessable_entity
      end
    end

    def destroy
      @node.destroy
      redirect_to admin_nodes_path, notice: "Node #{@node.callsign} deleted"
    end

    private

    def set_node
      @node = Node.find(params[:id])
    end

    def node_params
      params.require(:node).permit(:callsign, :node_class_id, :talkgroup_id, :node_location, :sysop, :rx_freq, :tx_freq, :locator, :monitored_tgs, :tone_to_talkgroup)
    end
  end
end
