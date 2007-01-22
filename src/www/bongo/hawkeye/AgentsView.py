import bongo.dragonfly
import Hawkeye
from HawkeyeHandler import HawkeyeHandler

class AgentsHandler(HawkeyeHandler):
    def ParsePath(self, rp):
        rp.pathInfo = Hawkeye.SplitQueryArgs(rp.path)

    def index_GET(self, req, rp):
        vars = {"mdb" : self.GetMdb(req)}
        return self.SendTemplate(req, rp, "index.psp", vars)

    def edit_GET(self, req, rp):
        vars = {"mdb" : self.GetMdb(req)}
        return self.SendTemplate(req, rp, "edit.psp", vars)

    def edit_POST(self, req, rp):
        vars = {"mdb" : self.GetMdb(req)}
        return self.SendTemplate(req, rp, "edit.psp", vars)
