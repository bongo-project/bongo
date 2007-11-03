import bongo.commonweb
import os
import bongo.commonweb.BongoUtil
from HawkeyeHandler import HawkeyeHandler
import bongo.hawkeye.Auth as Auth
from libbongo.libs import msgapi

AuthMode = 0

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
            # This is a non-critical error, so show to user as 'info', not 'error'
            self.SetVariable("info", "Unable to get memory statistics for your system. Please report this as a bug.")
            pass
        return 0.0

    def index_GET(self, req, rp):
        # template ui
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
        self.SetVariable("sw_current", sw_current)
        
        #sw_available = msgapi.GetAvailableVersion()
        #if (sw_available - build_ver) > 9:
            #self.SetVariable("sw_upgrade", 1) 
        #if sw_available == 0:
            #sw_available = "Unknown (no network, or DNS failure)"
        
        sw_available = "Disabled"
        self.SetVariable("sw_available", sw_available)
        
        # send the template
        return self.SendTemplate(req, rp, "index.tpl", title="Desktop")

    def login_GET(self, req, rp):
        global AuthMode
        
        if AuthMode == 1:
            self.SetVariable("badauth", 1)
            self.SetVariable("loggedout", 0)
            AuthMode = 0
        elif AuthMode == 2:
            self.SetVariable("badauth", 0)
            self.SetVariable("loggedout", 1)
            AuthMode = 0
        else:
            self.SetVariable("badauth", 0)
            self.SetVariable("loggedout", 0)
        
        return self.SendTemplate(req, rp, "login.tpl")

    def login_POST(self, req, rp):
        if not Auth.Login(req):
            global AuthMode
            
            AuthMode = 1
            return self.login_GET(req, rp)

        target = rp.tmplUriRoot

        if req.args and len(req.args) > 0:
            target = req.args

        return bongo.commonweb.BongoUtil.redirect(req, target)

    def logout_GET(self, req, rp):    
        if req.session:
            req.session.invalidate()
            req.session = None
            
            global AuthMode
            AuthMode = 2

        return bongo.commonweb.BongoUtil.redirect(req, "login")
