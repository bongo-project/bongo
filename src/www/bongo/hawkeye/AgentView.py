import bongo.dragonfly
from HawkeyeHandler import HawkeyeHandler

class AgentHandler(HawkeyeHandler):

    def index_GET(self, req, rp):
        self.SetVariable("breadcrumb", "Agents")
        self.SetVariable("agntab", "selecteditem")
        self.SetVariable("title", "Agents")
        return self.SendTemplate(req, rp, "index.tpl")
