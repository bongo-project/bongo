import bongo.dragonfly
import os
import bongo.dragonfly.BongoUtil
from HawkeyeHandler import HawkeyeHandler
import bongo.hawkeye.Auth as Auth
from bongo.libs import msgapi

class RootHandler(HawkeyeHandler):
    def NeedsAuth(self, rp):
        if rp.action == "login":
            return False
        else:
            return True

    def memory(self, VmKey):
        '''Return process memory usage in bytes.
        '''
        _scale = {'kB': 1024.0, 'mB': 1024.0*1024.0,
       'KB': 1024.0, 'MB': 1024.0*1024.0}

        try: # get the /proc/<pid>/status pseudo file
            t = open('/proc/meminfo')
            v = [v for v in t.readlines() if v.startswith(VmKey)]
            t.close()

             # convert Vm value to bytes
            if len(v) == 1:
               t = v[0].split()  # e.g. 'VmRSS:  9999  kB'
               if len(t) == 3:  ## and t[0] == VmKey:
                   return float(t[1]) * _scale.get(t[2], 0.0)
        except:
            self.SetVariable("error", "Unable to get memory statistics for your system.")
            pass
        return 0.0

    def index_GET(self, req, rp):
        # template ui
        self.SetVariable("breadcrumb", "Desktop")
        self.SetVariable("title", "Welcome!")
        self.SetVariable("dsktab", "selecteditem")
        # check ram free
        cm = self.memory("MemTotal:")
        used = cm - self.memory("MemFree:") - self.memory("Buffers:") - self.memory("Cached:")
	used = used / (1024*1024)
	cm = cm / (1024*1024)
        self.SetVariable("mem", str(round(used, 2)) + "MB / " + str(round(cm, 2)) + "MB")
        # check system load
        (rqmin1, rqmin5, rqmin15) = os.getloadavg()
	recent_av = (rqmin1 + rqmin5) / 2
        recent_load = "light"
        if recent_av > 0.7:
            recent_load = "moderate"
        if recent_av > 2:
            recent_load = "heavy"
        self.SetVariable("load", recent_load)
        # check for software updates
        (build_ver, build_custom) = msgapi.GetBuildVersion()
        sw_current = "%d" % build_ver
        if build_custom:
            sw_current += " (custom build)"
        sw_available = msgapi.GetAvailableVersion()
        self.SetVariable("sw_current", sw_current)
        self.SetVariable("sw_available", sw_available)
        if (sw_available - build_ver) > 9:
            self.SetVariable("sw_upgrade", 1) 
        # send the template
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
