#!/usr/bin/env groovy

/**
 *   Main file that is being picked by jenkins multibranch pipeline.
 *   Must only connect things common to all pipelines.
 *   Any custom logic must be defined in separate Jenkinsfiles declared at .ci/configuration.yml
 *
 *   Copied verbatim from jenkins-commons consumers (e.g. system-observability-service/Jenkinsfile) --
 *   this file is not project-specific, do not edit unless jenkins-commons' bootstrap contract changes.
 */

shared = null
newVersion = null
projectVersion = null
String repositoryUrl = null
String commitAuthor = null
String commitAuthorEmail = null

timestamps {
    try {
        node() {
            cleanWs()
            stage('Checkout & Bootstrap') {
                def result = checkoutGit()
                echo "[INFO] Git checkout: $result"
                repositoryUrl = result.GIT_URL
                env.GIT_COMMIT = result.GIT_COMMIT
                env.GIT_URL = result.GIT_URL

                commitAuthor = sh(script: 'git show -s --pretty=%an', returnStdout: true).trim()
                commitAuthorEmail = sh(script: 'git show -s --pretty=%ae', returnStdout: true).trim()
                echo "[INFO] Getting git info. Commit author: $commitAuthor <$commitAuthorEmail>"

                echo "[INFO] Building branch ${env.BRANCH_NAME}"

                def targetCommonsDirectory = '.ci/commons'
                if (fileExists(targetCommonsDirectory)) {
                    throw new IllegalStateException(".ci/commons must not exist")
                }
                shared = loadLibrary(targetCommonsDirectory)

                // Save folder to reuse on other nodes
                stash name: 'bootstrap', useDefaultExcludes: false

                shared.setDefaultPipelineConfiguration()

                def jenkinsFilePath = new File(currentBuild.rawBuild.parent.definition.scriptPath).parent
                def configurationFilePath = shared.defaultIfEmpty(jenkinsFilePath, '.') + '/.ci/configuration.yml'
                def configuration = null;
                if (fileExists(configurationFilePath)) {
                    configuration = readYaml(file: configurationFilePath)
                } else {
                    throw IllegalArgumentException("Pipeline configuration is not present")
                }

                pipelines = shared.configurePipelines(repositoryUrl, targetCommonsDirectory, configuration.pipelines)
            }
        }
        echo "[INFO] Found pipelines: $pipelines"
        pipelines.each { name, pipeline -> shared.executePipeline(name, pipeline) }
    } catch (reason) {
        echo "[ERROR] 🔴 Exception during build: $reason"
        reason.printStackTrace()
        currentBuild.result = 'FAILURE'
    } finally {
        node() {
            step([$class: 'ClaimPublisher'])
            cleanWs()

            if (currentBuild.currentResult != 'SUCCESS') {
                shared.reportFailure(repositoryUrl, commitAuthor, commitAuthorEmail)
            }
        }
    }
}

def loadLibrary(String targetCommonsDirectory) {
    echo "[INFO] Downloading common code"
    withCredentials([usernamePassword(credentialsId: 'github_repository_access', passwordVariable: 'GIT_TOKEN', usernameVariable: 'GIT_USERNAME')]) {
        sh "git clone https://$GIT_USERNAME:$GIT_TOKEN@github.com/leantechnologies/jenkins-commons.git ${targetCommonsDirectory}"
    }
    load "${targetCommonsDirectory}/shared.groovy"
}

/**
 * Default checkout scm doesn't download git tags and that breaks our release process
 */
def checkoutGit() {
    checkout([
            $class                           : 'GitSCM',
            branches                         : scm.branches,
            doGenerateSubmoduleConfigurations: scm.doGenerateSubmoduleConfigurations,
            extensions                       : [[$class: 'CloneOption', noTags: false, shallow: false, depth: 0, reference: '']],
            userRemoteConfigs                : scm.userRemoteConfigs,
    ])
}
