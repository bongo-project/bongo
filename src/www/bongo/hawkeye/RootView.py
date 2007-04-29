import bongo.dragonfly
import bongo.dragonfly.BongoUtil
from HawkeyeHandler import HawkeyeHandler
import bongo.hawkeye.Auth as Auth

class RootHandler(HawkeyeHandler):
    def NeedsAuth(self, rp):
        if rp.action == "login":
            return False
        else:
            return True

    def index_GET(self, req, rp):
        return self.SendTemplate(req, rp, "index.tpl")

    def test_GET(self, req, rp):
        self.SetVariable("helloworld", "Hello World!")
        self.SetVariable("escapeme", "If anything appears after this, I've been escaped. &nbsp;")
        self.SetVariable("greenstyle", "color: green")
        self.SetVariable("somethingtrue", 1)
        self.SetVariable("somethinguntrue", 0)
        self.SetVariable("testlist", [{ "say": "Hello!" }, { "say": "Bonjour!"}])
        return self.SendTemplate(req, rp, "test.tpl")

    def login_GET(self, req, rp):
        return self.SendTemplate(req, rp, "login.tpl")

    def login_POST(self, req, rp):
        if not Auth.Login(req):
            return self.login_GET(req, rp)

        target = rp.tmplUriRoot

        if req.args and len(req.args) > 0:
            target = req.args

        return bongo.dragonfly.BongoUtil.redirect(req, target)

    def logout_GET(self, req, rp):
        if req.session:
            req.session.invalidate()
            req.session = None

        return bongo.dragonfly.BongoUtil.redirect(req, "login")
