allprojects {
    repositories {
        google()
        mavenCentral()
    }
    configurations.configureEach {
        resolutionStrategy.eachDependency {
            if (requested.version?.endsWith("+") != true) return@eachDependency
            when ("${requested.group}:${requested.name}") {
                "androidx.test.espresso:espresso-core" -> useVersion("3.2.0")
                "androidx.test:runner" -> useVersion("1.2.0")
                "androidx.test:rules" -> useVersion("1.2.0")
            }
        }
    }
}

val newBuildDir: Directory =
    rootProject.layout.buildDirectory
        .dir("../../build")
        .get()
rootProject.layout.buildDirectory.value(newBuildDir)

subprojects {
    val newSubprojectBuildDir: Directory = newBuildDir.dir(project.name)
    project.layout.buildDirectory.value(newSubprojectBuildDir)
}
subprojects {
    project.evaluationDependsOn(":app")
}

tasks.register<Delete>("clean") {
    delete(rootProject.layout.buildDirectory)
}
