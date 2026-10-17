package lark.app

import lark.build.versionCode
import org.junit.Assert.assertEquals
import org.junit.Test

class SmokeTest {
    @Test
    fun versionCodeFromVersionName() {
        assertEquals(215, versionCode("0.2.15"))
        assertEquals(11003, versionCode("1.10.3"))
    }
}
